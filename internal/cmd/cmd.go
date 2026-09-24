// Package cmd implements doe's command line interface.
package cmd

import (
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
	"uuid"
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
	runScript := flags.StringP("run", "r", "", "shell script to run at each design point")
	if err := flags.Parse(args); errors.Is(err, pflag.ErrHelp) {
		experimentUsage(env.Stdout)
		return 0
	} else if err != nil {
		return env.Fail(err)
	}

	// Separate configuration errors from run failures, which may be cancellations.
	experiment, results, err := prepareExperiment(*file, *runScript, flags.Changed("run"), flags.Args())
	if err != nil {
		return env.Fail(err)
	}

	// Hold the lock while running so other processes can check for liveness.
	release, err := results.LockExperiment(experiment.ID)
	if err != nil {
		return env.Fail(err)
	}
	defer func() { _ = release() }()
	if err := results.AppendExperiment(experiment); err != nil {
		return env.Fail(err)
	}
	if err := runExperiment(ctx, experiment, results); err != nil {
		if ctx.Err() != nil {
			return 130
		}
		return env.Fail(err)
	}
	return 0
}

func prepareExperiment(path, runScript string, overrideRun bool, args []string) (*model.Experiment, *results.Results, error) {
	// Start from the file so CLI factors can override file factors.
	var study model.Study
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
	if overrideRun {
		experiment.Run = model.Script(runScript)
	}

	// Reject incomplete designs before running anything.
	if len(experiment.Factors) == 0 {
		return nil, nil, errors.New("at least one factor is required")
	}
	if strings.TrimSpace(string(experiment.Run)) == "" {
		return nil, nil, errors.New("a run script is required (use --run or a study file)")
	}

	// Keep scripts and results relative to the study file.
	dir := "."
	if path != "" {
		dir = filepath.Dir(path)
	}
	results, err := results.New(filepath.Join(dir, "results"))
	return &experiment, results, err
}

// runExperiment runs each point until a run fails.
func runExperiment(ctx context.Context, experiment *model.Experiment, results *results.Results) error {
	for _, point := range experiment.Points() {
		if err := runDesignPoint(ctx, experiment.Run, results, point); err != nil {
			return err
		}
	}
	return nil
}

func runDesignPoint(ctx context.Context, script model.Script, results *results.Results, point model.Point) error {
	// Avoid starting a shell when the experiment is already canceled.
	if err := ctx.Err(); err != nil {
		return err
	}
	log, err := results.OpenRunLog(uuid.NewV7())
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "/bin/sh", "-c", expandRunScript(script, point))
	command.Dir = results.ProjectDir()
	command.Stdout = log
	command.Stderr = log
	runErr := command.Run()
	closeErr := log.Close()
	if runErr != nil {
		return fmt.Errorf("run %v: %w", point, runErr)
	}
	return closeErr
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

Usage: doe experiment [-f study.yaml] [key=value ...] [-r script]

Options:
  -f, --file  Load factors and run script from a YAML study.
  -r, --run   Override the run script with a shell command.
  -h, --help  Print help text.

Factor settings are YAML values or sequences of values. CLI factors override
file factors. In run scripts, {factor} expands to the setting.
`)
}
