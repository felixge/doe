// Package cmd implements doe's command line interface.
package cmd

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/felixge/doe/internal/cli"
	"github.com/felixge/doe/internal/model"
	"github.com/felixge/doe/internal/results"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

// Main executes the doe command.
func Main(ctx context.Context, env *cli.Env, args []string) int {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(args) == 0 {
		rootUsage(env.Stdout)
		return 0
	}
	switch args[0] {
	case "-h", "--help", "help":
		rootUsage(env.Stdout)
		return 0
	case "run":
		return runCommandHandler(ctx, env, args[1:])
	case "status":
		return statusCommand(env, args[1:])
	default:
		_, _ = fmt.Fprintf(env.Stderr, "unknown command: %s\n", args[0])
		return 1
	}
}

func runCommandHandler(ctx context.Context, env *cli.Env, args []string) int {
	// Parse flags.
	flags := pflag.NewFlagSet("doe run", pflag.ContinueOnError)
	flags.SetOutput(env.Stderr)
	flags.SetInterspersed(true)
	flags.Usage = func() {}
	file := flags.StringP("file", "f", "", "study YAML file")
	preset := flags.StringP("preset", "p", "", "named study preset")
	setupScript := flags.StringP("setup", "s", "", "shell script to run before any runs")
	runScript := flags.StringP("run", "r", "", "shell script to run at each design point")
	reportScript := flags.String("report", "", "shell script to run after all runs succeed")
	replicates := flags.IntP("replicates", "n", 0, "runs per design point")
	clean := flags.BoolP("clean", "c", false, "remove previous results before running")
	noReuse := flags.Bool("no-reuse", false, "execute all runs instead of reusing successful runs")
	design := flags.Bool("design", false, "print the resolved design as YAML without running")
	if err := flags.Parse(args); errors.Is(err, pflag.ErrHelp) {
		runUsage(env.Stdout)
		return 0
	} else if err != nil {
		return env.Fail(err)
	}

	// Prepare and validate the experiment before starting any runs.
	experiment, err := prepareExperiment(*file, *preset, *setupScript, *runScript, *reportScript, *replicates, flags.Args())
	if err != nil {
		return env.Fail(err)
	}
	if *design {
		data, err := yaml.Marshal(experiment.Design)
		if err != nil {
			return env.Fail(err)
		}
		if _, err := env.Stdout.Write(data); err != nil {
			return env.Fail(err)
		}
		return 0
	}

	// Validate before touching previous results, and lock before cleaning them.
	r, release, err := results.Open(resultsDir(*file), experiment.ID, *clean)
	if err != nil {
		return env.Fail(err)
	}
	defer func() { _ = release() }()
	setupLog, err := r.CreateExperiment(experiment)
	if err != nil {
		return env.Fail(err)
	}
	defer func() { _ = setupLog.Close() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runExperiment(ctx, experiment, r, filepath.Dir(resultsDir(*file)), setupLog, !*noReuse)
	}()
	if isTerminal(env.Stdout) {
		err = watchExperiment(env.Stdout, r, done, cancel)
	} else {
		_, _ = fmt.Fprintln(env.Stdout, experiment.ID)
		err = <-done
	}
	if err != nil {
		return env.Fail(err)
	}
	return 0
}

func statusCommand(env *cli.Env, args []string) int {
	flags := pflag.NewFlagSet("doe status", pflag.ContinueOnError)
	flags.SetOutput(env.Stderr)
	flags.Usage = func() {}
	file := flags.StringP("file", "f", "", "study YAML file")
	if err := flags.Parse(args); errors.Is(err, pflag.ErrHelp) {
		statusUsage(env.Stdout)
		return 0
	} else if err != nil {
		return env.Fail(err)
	}
	if len(flags.Args()) != 0 {
		return env.Fail(fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " ")))
	}
	r := results.OpenReadOnly(resultsDir(*file))
	status, err := r.Status()
	if err != nil {
		return env.Fail(err)
	}
	if status == nil {
		return env.Fail(fmt.Errorf("no experiment found in %s", resultsDir(*file)))
	}
	_, _ = io.WriteString(env.Stdout, formatStatus(status, r))
	return 0
}

func formatStatus(status *results.Status, r *results.Results) string {
	var output strings.Builder
	_, _ = fmt.Fprintf(&output, "Experiment: %s\nState: %s\n", status.ExperimentID, status.State)
	if status.State == results.StateSetup {
		_, _ = fmt.Fprintf(&output, "Setup log: %s\n", r.SetupLogPath(status.ExperimentID))
	}
	if status.State == results.StateReporting {
		_, _ = fmt.Fprintf(&output, "Report log: %s\n", r.ReportLogPath(status.ExperimentID))
	}
	if status.Total > 0 {
		_, _ = fmt.Fprintf(&output, "Runs: %d/%d complete (%d%%)\n", status.Completed, status.Total, status.Completed*100/status.Total)
	}
	if status.State == results.StateError {
		_, _ = fmt.Fprintf(&output, "Error: %s\n", status.Error)
	}
	if status.RunReplicate > 0 {
		_, _ = fmt.Fprintf(&output, "Current run: replicate %d, design point %s\n", status.RunReplicate, status.RunPoint)
	}
	if status.RunElapsed > 0 {
		_, _ = fmt.Fprintf(&output, "Run elapsed: %s\n", formatDuration(status.RunElapsed))
	}
	if status.State == results.StateRunning && status.RunLogPath != "" {
		_, _ = fmt.Fprintf(&output, "Run log: %s\n", status.RunLogPath)
	}
	if status.ExperimentElapsed > 0 {
		_, _ = fmt.Fprintf(&output, "Experiment elapsed: %s\n", formatDuration(status.ExperimentElapsed))
	}
	if status.Remaining > 0 {
		_, _ = fmt.Fprintf(&output, "Experiment remaining: %s\n", formatDuration(status.Remaining))
	}
	return output.String()
}

func formatDuration(duration time.Duration) string {
	duration = max(duration.Round(time.Second), 0)
	hours := duration / time.Hour
	duration %= time.Hour
	minutes := duration / time.Minute
	seconds := duration % time.Minute / time.Second
	parts := make([]string, 0, 3)
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if seconds > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}
	return strings.Join(parts, " ")
}

func prepareExperiment(path, preset, setupScript, runScript, reportScript string, replicates int, args []string) (*model.Experiment, error) {
	// Keep each source separate so only its specified options override earlier ones.
	study := model.NewStudy()
	if path != "" {
		if err := study.Load(path); err != nil {
			return nil, err
		}
	}
	var presetDesign model.Design
	if preset != "" {
		var ok bool
		presetDesign, ok = study.Presets[preset]
		if !ok {
			return nil, fmt.Errorf("unknown preset %q", preset)
		}
	}
	cliDesign := model.Design{Setup: model.Script(setupScript), Run: model.Script(runScript), Report: model.Script(reportScript), Replicates: replicates}
	for _, arg := range args {
		factor, settingsYAML, ok := strings.Cut(arg, "=")
		if !ok {
			return nil, fmt.Errorf("factor %q must be key=value", arg)
		}
		if err := cliDesign.Factors.Set(model.Factor(factor), []byte(settingsYAML)); err != nil {
			return nil, err
		}
	}

	// Apply the built-in default only after merging, so zero means absent in a preset.
	design := study.Design.Merge(presetDesign).Merge(cliDesign)
	design.Replicates = cmp.Or(design.Replicates, 1)
	design.Setup = model.Script(strings.TrimSpace(string(design.Setup)))
	design.Run = model.Script(strings.TrimSpace(string(design.Run)))
	design.Report = model.Script(strings.TrimSpace(string(design.Report)))
	if err := design.Validate(); err != nil {
		return nil, err
	}
	experiment := model.NewExperiment(design)
	experiment.Preset = preset
	return &experiment, nil
}

func resultsDir(path string) string {
	dir := "."
	if path != "" {
		dir = filepath.Dir(path)
	}
	return filepath.Join(dir, "results")
}

// runExperiment updates records throughout the experiment lifecycle and closes setupLog.
func runExperiment(ctx context.Context, experiment *model.Experiment, r *results.Results, projectDir string, setupLog *os.File, reuse bool) error {
	var setupExecErr, setupJSONErr error
	if experiment.Setup != "" {
		setupExecErr = executeScript(ctx, string(experiment.Setup), projectDir, setupLog)
		experiment.Env, setupJSONErr = lastJSON[model.Env](setupLog, false)
	}
	setupCloseErr := setupLog.Close()
	experiment.SetupEnd = time.Now()

	if err := errors.Join(ctx.Err(), setupExecErr, setupJSONErr, setupCloseErr); err != nil {
		return finishExperiment(experiment, r, fmt.Errorf("setup: %w", err))
	}
	index, err := r.OpenRunIndex()
	if err != nil {
		return finishExperiment(experiment, r, err)
	}
	defer func() { _ = index.Close() }()

	// Copy reusable runs before executing new ones so progress estimates only
	// include pending work, even when reused points occur later in the schedule.
	type pendingRun struct {
		point     model.Point
		replicate int
		hash      string
	}
	var pending []pendingRun
	for replicate, row := range model.Schedule(len(experiment.Points), experiment.Replicates) {
		for _, pointIndex := range row {
			if err := ctx.Err(); err != nil {
				return finishExperiment(experiment, r, err)
			}
			point := experiment.Points[pointIndex]
			hash, err := experiment.RunHash(point, replicate+1)
			if err != nil {
				return finishExperiment(experiment, r, err)
			}
			if reuse {
				copied, err := index.Reuse(experiment.ID, hash)
				if err != nil {
					return finishExperiment(experiment, r, err)
				}
				if copied {
					continue
				}
			}
			pending = append(pending, pendingRun{point, replicate + 1, hash})
		}
	}
	experiment.State = model.StateRunning
	if err := r.UpdateExperiment(experiment); err != nil {
		return err
	}
	for _, run := range pending {
		if err := runDesignPoint(ctx, experiment, r, index, projectDir, run.point, run.replicate, run.hash); err != nil {
			return finishExperiment(experiment, r, err)
		}
	}
	if experiment.Report != "" {
		if err := runReport(ctx, experiment, r, projectDir); err != nil {
			return finishExperiment(experiment, r, fmt.Errorf("report: %w", err))
		}
	}
	return finishExperiment(experiment, r, ctx.Err())
}

// runReport runs only after all run records have been saved successfully.
func runReport(ctx context.Context, experiment *model.Experiment, r *results.Results, projectDir string) error {
	dir, err := filepath.Abs(r.ExperimentDir(experiment.ID))
	if err != nil {
		return err
	}
	log, err := r.CreateReportLog(experiment.ID)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	experiment.State = model.StateReporting
	if err := r.UpdateExperiment(experiment); err != nil {
		return err
	}
	executionErr := executeScript(ctx, string(experiment.Report), projectDir, log, "DOE_EXPERIMENT_DIR="+dir)
	return errors.Join(ctx.Err(), executionErr, log.Close())
}

func finishExperiment(experiment *model.Experiment, r *results.Results, err error) error {
	experiment.State, experiment.Error = stopReason(err)
	experiment.End = time.Now()
	return errors.Join(err, r.UpdateExperiment(experiment))
}

func runDesignPoint(ctx context.Context, experiment *model.Experiment, r *results.Results, index *results.RunIndex, projectDir string, point model.Point, replicate int, hash string) error {
	run := model.NewRun(experiment.ID, point, replicate)
	log, err := r.CreateRun(run)
	if err != nil {
		return fmt.Errorf("run %v: %w", point, err)
	}

	executionErr := executeScript(ctx, expandRunScript(experiment.Run, point), projectDir, log)
	outcome, outcomeErr := lastJSON[model.Outcome](log, true)
	closeErr := log.Close()
	err = errors.Join(ctx.Err(), executionErr, outcomeErr, closeErr)
	run.State, run.Error = stopReason(err)
	run.Outcome = outcome
	run.End = time.Now()
	err = errors.Join(err, r.UpdateRun(run))
	if err == nil {
		err = index.Add(hash, run)
	}
	if err != nil {
		return fmt.Errorf("run %v: %w", point, err)
	}
	return nil
}

// executeScript streams both output streams to log. The caller closes the log.
// Extra environment variables override inherited values.
func executeScript(ctx context.Context, script, projectDir string, log *os.File, env ...string) error {
	command := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	command.Dir = projectDir
	command.Env = append(command.Environ(), env...)
	command.Stdout = log
	command.Stderr = log
	return command.Run()
}

// lastJSON reads the final JSON object, returning an empty object when optional JSON is absent.
func lastJSON[T ~map[K]V, K comparable, V any](log *os.File, required bool) (T, error) {
	last, err := lastLogLine(log)
	if err != nil {
		return nil, err
	}
	var object T
	if err := json.Unmarshal(last, &object); err == nil && object != nil {
		return object, nil
	} else if !required {
		return T{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("run log %s has no JSON object on its last line: %w", log.Name(), err)
	}
	return nil, fmt.Errorf("run log %s has no JSON object on its last line", log.Name())
}

func lastLogLine(log *os.File) ([]byte, error) {
	info, err := log.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat log %s: %w", log.Name(), err)
	}
	// Expand the tail window until it contains the last line's start.
	for size := min(info.Size(), 4096); ; size = min(size*2, info.Size()) {
		tail := make([]byte, size)
		if _, err := log.ReadAt(tail, info.Size()-size); err != nil {
			return nil, fmt.Errorf("read log %s: %w", log.Name(), err)
		}
		tail = bytes.TrimSuffix(tail, []byte{'\n'})
		if index := bytes.LastIndexByte(tail, '\n'); index >= 0 {
			return tail[index+1:], nil
		}
		if size == info.Size() {
			return tail, nil
		}
	}
}

// stopReason maps an error to a terminal state. Only cancellation means stopped;
// a deadline is recorded as an error.
func stopReason(err error) (model.State, string) {
	switch {
	case err == nil:
		return model.StateDone, ""
	case errors.Is(err, context.Canceled):
		return model.StateStopped, ""
	default:
		return model.StateError, err.Error()
	}
}

func expandRunScript(script model.Script, point model.Point) string {
	return script.Expand(point)
}

func rootUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `A lightweight CLI for design of experiments studies.

Usage: doe <command> [command options] [arguments]

Commands:
  run     Run an experiment and save run logs.
  status  Show the latest experiment's progress.

Study files:
  A study is a YAML file with scripts, factors, and optional named presets.
  Each combination of factor settings is a design point. An experiment is
  one "doe run" invocation, with each point run replicates times (default: 1).

Example study.yaml:
  setup: ./setup.bash
  run: ./run.bash {algorithm} {effort}
  report: ./report.bash
  factors:
    algorithm: [gzip, zstd]
    effort: [default]
  presets:
    smoke: {}
    full:
      factors:
        effort: [min, max]
      replicates: 6

  Presets inherit study settings. Preset factors override individual factors;
  CLI options override both. Factor settings can be YAML scalars or sequences.
  To change a study, edit its scripts, factors, replicates, or preset overrides.

Scripts:
  Scripts are shell commands executed in the study file's directory.
  Optional setup runs once before any runs; its final stdout line can be a
  JSON object describing the environment. Run must end stdout with a JSON
  object of measurements, e.g. {"seconds":0.12}, and exit successfully.
  Every factor needs a {factor} placeholder in run. Substitution is literal,
  not shell-escaped. Setup and report do not expand placeholders.
  Optional report runs once after all runs succeed, including reused runs.
  DOE_EXPERIMENT_DIR gives report the absolute experiment directory path.
  A report failure fails the experiment but does not invalidate successful runs.

Workflow:
  doe run -f study.yaml -p full --design       # Preview resolved YAML; do not run.
  doe run -f study.yaml -p smoke               # Run a small experiment.
  doe run -f study.yaml -p full -n 2 'effort=[min, max]'  # Override replicates and factors.
  doe status -f study.yaml                    # Show progress and active log path.

  Successful runs with matching setup/run commands, point, and replicate
  are reused. Script file contents and dependencies are NOT tracked: use
  --no-reuse after changing them. Use -c to delete previous results first.

Results (next to the study file):
  results/experiments/<experiment-id>/experiment.json
  results/experiments/<experiment-id>/setup.log
  results/experiments/<experiment-id>/report.log (when report is configured)
  results/experiments/<experiment-id>/runs/<run-id>.{json,log}
  Run JSON stores inputs under point and measurements under outcome.
  Analyze only runs with state = "done"; group by point to compare settings.

Run "doe <command> -h" for command-specific help.
`)
}

func statusUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Show the latest experiment's progress.

Usage: doe status [-f study.yaml]

Options:
  -f, --file  Read results next to the study YAML file.
  -h, --help  Print help text.
`)
}

func runUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Run one script for each combination of factor settings.

Usage: doe run [-f study.yaml] [-p name] [key=value ...] [-s script] [-r script] [--report script] [-n count] [-c] [--no-reuse] [--design]

Options:
  -f, --file        Load factors, scripts, and replicates from a YAML study.
  -p, --preset      Apply a named preset from the study.
  -s, --setup       Override the setup script, run once before any runs.
  -r, --run         Override the run script with a shell command.
      --report      Override the optional report script, run after all runs succeed.
  -n, --replicates  Override runs per design point (default: 1).
  -c, --clean       Remove previous results before running.
      --no-reuse    Execute all runs instead of reusing successful runs.
      --design      Print the resolved design as YAML without running.
  -h, --help        Print help text.

Factor settings are YAML values or sequences of values. Study defaults are
inherited by the selected preset; CLI options override both. In run scripts,
{factor} expands to the setting.
Setup and report scripts do not expand factor placeholders.
Report runs once after success, including reused runs, and requires no JSON.
DOE_EXPERIMENT_DIR gives report the absolute experiment directory path.
Report stdout and stderr go to report.log; report failure fails the experiment.
A study's replicates key runs each design point that many times (default: 1).
Successful runs with matching scripts, point, and replicate are reused by default.
`)
}
