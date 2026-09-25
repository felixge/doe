// Package cmd implements doe's command line interface.
package cmd

import (
	"cmp"
	"context"
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
	"uuid"
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
	case "experiment":
		return experimentCommand(ctx, env, args[1:])
	case "status":
		return statusCommand(env, args[1:])
	default:
		_, _ = fmt.Fprintf(env.Stderr, "unknown command: %s\n", args[0])
		return 1
	}
}

func experimentCommand(ctx context.Context, env *cli.Env, args []string) int {
	// Parse flags.
	flags := pflag.NewFlagSet("doe experiment", pflag.ContinueOnError)
	flags.SetOutput(env.Stderr)
	flags.SetInterspersed(true)
	flags.Usage = func() {}
	file := flags.StringP("file", "f", "", "study YAML file")
	preset := flags.StringP("preset", "p", "", "named study preset")
	setupScript := flags.StringP("setup", "s", "", "shell script to run before any runs")
	runScript := flags.StringP("run", "r", "", "shell script to run at each design point")
	replicates := flags.IntP("replicates", "n", 0, "runs per design point")
	clean := flags.BoolP("clean", "c", false, "remove previous results before running")
	design := flags.Bool("design", false, "print the resolved design as YAML without running")
	if err := flags.Parse(args); errors.Is(err, pflag.ErrHelp) {
		experimentUsage(env.Stdout)
		return 0
	} else if err != nil {
		return env.Fail(err)
	}

	// Prepare and validate the experiment before starting any runs.
	experiment, err := prepareExperiment(*file, *preset, *setupScript, *runScript, *replicates, flags.Args())
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
	results, err := results.New(resultsDir(*file))
	if err != nil {
		return env.Fail(err)
	}

	// Hold the lock while running so other processes can check for liveness.
	release, err := results.LockExperiment(experiment.ID)
	if err != nil {
		return env.Fail(err)
	}
	defer func() { _ = release() }()
	if *clean {
		if err := results.Clean(); err != nil {
			return env.Fail(err)
		}
	}
	if err := runExperiment(ctx, experiment, results); err != nil {
		if ctx.Err() != nil {
			return 130
		}
		return env.Fail(err)
	}
	_, _ = fmt.Fprintln(env.Stdout, experiment.ID)
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
	status, err := results.Open(resultsDir(*file)).Status()
	if err != nil {
		return env.Fail(err)
	}
	if status == nil {
		return env.Fail(fmt.Errorf("no experiment found in %s", resultsDir(*file)))
	}
	_, _ = fmt.Fprintf(env.Stdout, "Experiment: %s\nState: %s\n", status.ExperimentID, status.State)
	if status.Total > 0 {
		_, _ = fmt.Fprintf(env.Stdout, "Runs: %d/%d complete (%d%%)\n", status.Completed, status.Total, status.Completed*100/status.Total)
	}
	if status.State == results.StateError {
		_, _ = fmt.Fprintf(env.Stdout, "Error: %s\n", status.Error)
	}
	if status.RunReplicate > 0 {
		_, _ = fmt.Fprintf(env.Stdout, "Current run: replicate %d, design point %s\n", status.RunReplicate, status.RunPoint)
	}
	if status.RunElapsed > 0 {
		_, _ = fmt.Fprintf(env.Stdout, "Run elapsed: %s\n", formatDuration(status.RunElapsed))
	}
	if status.ExperimentElapsed > 0 {
		_, _ = fmt.Fprintf(env.Stdout, "Experiment elapsed: %s\n", formatDuration(status.ExperimentElapsed))
	}
	if status.Remaining > 0 {
		_, _ = fmt.Fprintf(env.Stdout, "Experiment remaining: %s\n", formatDuration(status.Remaining))
	}
	return 0
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

func prepareExperiment(path, preset, setupScript, runScript string, replicates int, args []string) (*model.Experiment, error) {
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
	cliDesign := model.Design{Setup: model.Script(setupScript), Run: model.Script(runScript), Replicates: replicates}
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

// runExperiment records the setup environment before starting any runs.
func runExperiment(ctx context.Context, experiment *model.Experiment, results *results.Results) error {
	var setupErr error
	if experiment.Setup != "" {
		experiment.Env, setupErr = runSetup(ctx, results, experiment.ID, experiment.Setup)
		if setupErr != nil && ctx.Err() == nil {
			experiment.SetupError = setupErr.Error()
		}
	}
	if err := results.AppendExperiment(experiment); err != nil || setupErr != nil {
		return errors.Join(setupErr, err)
	}
	for replicate, row := range model.Schedule(len(experiment.Points), experiment.Replicates) {
		for _, index := range row {
			if err := runDesignPoint(ctx, experiment, results, experiment.Points[index], replicate+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func runSetup(ctx context.Context, results *results.Results, experimentID uuid.UUID, script model.Script) (model.Env, error) {
	if err := ctx.Err(); err != nil {
		return model.Env{}, err
	}
	log, err := results.CreateSetupLog(experimentID)
	if err != nil {
		return model.Env{}, err
	}
	if err := executeScript(ctx, string(script), results, log); err != nil {
		return model.Env{}, fmt.Errorf("setup: %w", err)
	}
	environment, err := results.SetupEnv(experimentID)
	if err != nil {
		return model.Env{}, fmt.Errorf("setup: %w", err)
	}
	return environment, nil
}

func runDesignPoint(ctx context.Context, experiment *model.Experiment, results *results.Results, point model.Point, replicate int) error {
	// Avoid starting a shell when the experiment is already canceled.
	if err := ctx.Err(); err != nil {
		return err
	}
	run := model.NewRun(experiment.ID, point, replicate)
	log, err := results.CreateRunLog(run.ID)
	if err != nil {
		return err
	}
	err = executeScript(ctx, expandRunScript(experiment.Run, point), results, log)
	// An interrupted run has no outcome. Keep its log, but do not record it
	// as a failed run: status should report the experiment as stopped.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	run.End = time.Now()
	outcome, outcomeErr := results.RunOutcome(run.ID)
	run.Outcome = outcome
	validationErr := run.Valid()
	if validationErr != nil {
		run.Outcome = nil
	}
	err = errors.Join(err, outcomeErr, validationErr)
	if err != nil {
		run.Error = err.Error()
	}
	err = errors.Join(err, results.AppendRun(run))
	if err != nil {
		return fmt.Errorf("run %v: %w", point, err)
	}
	return nil
}

// executeScript streams both output streams to the log and closes it even on failure.
func executeScript(ctx context.Context, script string, results *results.Results, log *os.File) error {
	command := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	command.Dir = results.ProjectDir()
	command.Stdout = log
	command.Stderr = log
	return cmp.Or(command.Run(), log.Close())
}

func expandRunScript(script model.Script, point model.Point) string {
	return script.Expand(point)
}

func rootUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `A lightweight CLI for design of experiments studies.

Usage: doe <command> [command options] [arguments]

Commands:
  experiment  Run an experiment and save run logs.
  status      Show the latest experiment's progress.

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

func experimentUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Run one script for each combination of factor settings.

Usage: doe experiment [-f study.yaml] [-p name] [key=value ...] [-s script] [-r script] [-n count] [-c] [--design]

Options:
  -f, --file        Load factors, scripts, and replicates from a YAML study.
  -p, --preset      Apply a named preset from the study.
  -s, --setup       Override the setup script, run once before any runs.
  -r, --run         Override the run script with a shell command.
  -n, --replicates  Override runs per design point (default: 1).
  -c, --clean       Remove previous results before running.
      --design      Print the resolved design as YAML without running.
  -h, --help        Print help text.

Factor settings are YAML values or sequences of values. Study defaults are
inherited by the selected preset; CLI options override both. In run scripts,
{factor} expands to the setting.
Setup scripts do not expand factor placeholders.
A study's replicates key runs each design point that many times (default: 1).
`)
}
