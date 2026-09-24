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
	case "clean":
		return cleanCommand(env, args[1:])
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
	setupScript := flags.StringP("setup", "s", "", "shell script to run before any runs")
	runScript := flags.StringP("run", "r", "", "shell script to run at each design point")
	replicates := flags.IntP("replicates", "n", 1, "runs per design point")
	if err := flags.Parse(args); errors.Is(err, pflag.ErrHelp) {
		experimentUsage(env.Stdout)
		return 0
	} else if err != nil {
		return env.Fail(err)
	}

	// Validate the study before recording an experiment or starting any runs.
	experiment, results, err := prepareExperiment(*file, *setupScript, *runScript, *replicates, flags.Changed("replicates"), flags.Args())
	if err != nil {
		return env.Fail(err)
	}

	// Hold the lock while running so other processes can check for liveness.
	release, err := results.LockExperiment(experiment.ID)
	if err != nil {
		return env.Fail(err)
	}
	defer func() { _ = release() }()
	if err := runExperiment(ctx, experiment, results); err != nil {
		if ctx.Err() != nil {
			return 130
		}
		return env.Fail(err)
	}
	_, _ = fmt.Fprintln(env.Stdout, experiment.ID)
	return 0
}

func prepareExperiment(path, setupScript, runScript string, replicates int, overrideReplicates bool, args []string) (*model.Experiment, *results.Results, error) {
	// Start from the file so CLI options can override study settings.
	study := model.NewStudy()
	if path != "" {
		if err := study.Load(path); err != nil {
			return nil, nil, err
		}
	}
	experiment := model.NewExperiment(study)
	for _, arg := range args {
		factor, settingsYAML, ok := strings.Cut(arg, "=")
		if !ok {
			return nil, nil, fmt.Errorf("factor %q must be key=value", arg)
		}
		if err := experiment.Factors.Set(model.Factor(factor), []byte(settingsYAML)); err != nil {
			return nil, nil, err
		}
	}
	if setupScript != "" {
		experiment.Setup = model.Script(setupScript)
	}
	experiment.Setup = model.Script(strings.TrimSpace(string(experiment.Setup)))
	if runScript != "" {
		experiment.Run = model.Script(runScript)
	}
	experiment.Run = model.Script(strings.TrimSpace(string(experiment.Run)))
	if overrideReplicates {
		experiment.Replicates = replicates
	}

	// Reject incomplete designs before running anything.
	if err := experiment.Validate(); err != nil {
		return nil, nil, err
	}

	// Keep scripts and results relative to the study file.
	results, err := results.New(resultsDir(path))
	return &experiment, results, err
}

func cleanCommand(env *cli.Env, args []string) int {
	flags := pflag.NewFlagSet("doe clean", pflag.ContinueOnError)
	flags.SetOutput(env.Stderr)
	flags.Usage = func() {}
	file := flags.StringP("file", "f", "", "study YAML file")
	if err := flags.Parse(args); errors.Is(err, pflag.ErrHelp) {
		cleanUsage(env.Stdout)
		return 0
	} else if err != nil {
		return env.Fail(err)
	}
	if len(flags.Args()) != 0 {
		return env.Fail(fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " ")))
	}
	if err := os.RemoveAll(resultsDir(*file)); err != nil {
		return env.Fail(fmt.Errorf("remove results directory: %w", err))
	}
	return 0
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
	experiment.Env = model.Env{}
	if experiment.Setup != "" {
		if err := runSetup(ctx, experiment, results); err != nil {
			return err
		}
	}
	if err := results.AppendExperiment(experiment); err != nil {
		return err
	}
	points := experiment.Points()
	for replicate, row := range model.Schedule(len(points), experiment.Replicates) {
		for _, index := range row {
			if err := runDesignPoint(ctx, experiment, results, points[index], replicate+1); err != nil {
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
	if err := results.AppendRun(&run); err != nil {
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
  clean       Remove the results directory.

Run "doe <command> -h" for command-specific help.
`)
}

func cleanUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Remove the results directory for a study or the current directory.

Usage: doe clean [-f study.yaml]

Options:
  -f, --file  Select the project directory containing the study file.
  -h, --help  Print help text.
`)
}

func experimentUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Run one script for each combination of factor settings.

Usage: doe experiment [-f study.yaml] [key=value ...] [-s script] [-r script] [-n count]

Options:
  -f, --file        Load factors, scripts, and replicates from a YAML study.
  -s, --setup       Override the setup script, run once before any runs.
  -r, --run         Override the run script with a shell command.
  -n, --replicates  Override runs per design point (default: 1).
  -h, --help        Print help text.

Factor settings are YAML values or sequences of values. CLI options override
study settings. In run scripts, {factor} expands to the setting.
Setup scripts do not expand factor placeholders.
A study's replicates key runs each design point that many times (default: 1).
`)
}
