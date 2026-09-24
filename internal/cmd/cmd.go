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
	"regexp"
	"strings"
	"syscall"

	"github.com/felixge/doe2/internal/cli"
	"github.com/felixge/doe2/internal/model"
	"github.com/felixge/doe2/internal/results"
	"github.com/spf13/pflag"
)

var factorPlaceholder = regexp.MustCompile(`\{[a-zA-Z_][a-zA-Z0-9_]*\}`)

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
	if experiment.Setup != "" {
		if err := runSetup(ctx, experiment, results); err != nil {
			return err
		}
	}
	if err := results.AppendExperiment(experiment); err != nil {
		return err
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

func runSetup(ctx context.Context, experiment *model.Experiment, results *results.Results) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	log, err := results.CreateSetupLog(experiment.ID)
	if err != nil {
		return err
	}
	if err := executeScript(ctx, string(experiment.Setup), results, log); err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	environment, err := results.SetupEnv(experiment.ID)
	if err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	experiment.Env = environment
	return nil
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
	if err := executeScript(ctx, expandRunScript(experiment.Run, point), results, log); err != nil {
		return fmt.Errorf("run %v: %w", point, err)
	}
	outcome, err := results.RunOutcome(run.ID)
	if err != nil {
		return fmt.Errorf("run %v: %w", point, err)
	}
	run.Outcome = outcome
	if err := results.AppendRun(run); err != nil {
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
	return factorPlaceholder.ReplaceAllStringFunc(string(script), func(placeholder string) string {
		setting, ok := point[model.Factor(placeholder[1:len(placeholder)-1])]
		if !ok {
			return placeholder
		}
		return fmt.Sprint(setting)
	})
}

func rootUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `A lightweight CLI for design of experiments studies.

Usage: doe <command> [command options] [arguments]

Commands:
  experiment  Run an experiment and save run logs.

Run "doe <command> -h" for command-specific help.
`)
}

func experimentUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Run one script for each combination of factor settings.

Usage: doe experiment [-f study.yaml] [-p name] [key=value ...] [-s script] [-r script] [-n count] [-c]

Options:
  -f, --file        Load factors, scripts, and replicates from a YAML study.
  -p, --preset      Apply a named preset from the study.
  -s, --setup       Override the setup script, run once before any runs.
  -r, --run         Override the run script with a shell command.
  -n, --replicates  Override runs per design point (default: 1).
  -c, --clean       Remove previous results before running.
  -h, --help        Print help text.

Factor settings are YAML values or sequences of values. Study defaults are
inherited by the selected preset; CLI options override both. In run scripts,
{factor} expands to the setting.
Setup scripts do not expand factor placeholders.
A study's replicates key runs each design point that many times (default: 1).
`)
}
