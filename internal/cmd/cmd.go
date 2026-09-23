// Package cmd implements doe's command line interface.
package cmd

import (
	"context"
	"encoding/json"
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
	runScript := flags.StringP("run", "r", "", "shell script to run at each design point")
	if err := flags.Parse(args); errors.Is(err, pflag.ErrHelp) {
		experimentUsage(env.Stdout)
		return 0
	} else if err != nil {
		return env.Fail(err)
	}

	// Separate configuration errors from run failures, which may be cancellations.
	experiment, dir, err := prepareExperiment(*file, *runScript, flags.Changed("run"), flags.Args())
	if err != nil {
		return env.Fail(err)
	}

	// Record only completed experiments; failed runs leave no record.
	if err := runExperiment(ctx, env, experiment, dir); err != nil {
		if ctx.Err() != nil {
			return 130
		}
		return env.Fail(err)
	}
	if err := appendExperiment(experiment, dir); err != nil {
		return env.Fail(err)
	}
	return 0
}

func prepareExperiment(path, runScript string, overrideRun bool, args []string) (model.Experiment, string, error) {
	// Start from the file so CLI factors can override file factors.
	var study model.Study
	if path != "" {
		if err := study.Load(path); err != nil {
			return model.Experiment{}, "", err
		}
	}
	experiment := model.NewExperiment(study)
	for _, arg := range args {
		factor, settingsYAML, ok := strings.Cut(arg, "=")
		if !ok {
			return model.Experiment{}, "", fmt.Errorf("factor %q must be key=value", arg)
		}
		if err := experiment.Factors.Set(model.Factor(factor), []byte(settingsYAML)); err != nil {
			return model.Experiment{}, "", err
		}
	}
	if overrideRun {
		experiment.Run = model.Script(runScript)
	}

	// Reject incomplete designs before running anything.
	if len(experiment.Factors) == 0 {
		return model.Experiment{}, "", errors.New("at least one factor is required")
	}
	if strings.TrimSpace(string(experiment.Run)) == "" {
		return model.Experiment{}, "", errors.New("a run script is required (use --run or a study file)")
	}

	// Keep scripts and results relative to the study file.
	dir := "."
	if path != "" {
		dir = filepath.Dir(path)
	}
	return experiment, dir, nil
}

func appendExperiment(experiment model.Experiment, dir string) error {
	resultsDir := filepath.Join(dir, "results")
	if err := os.MkdirAll(resultsDir, 0755); err != nil {
		return fmt.Errorf("create results directory: %w", err)
	}
	path := filepath.Join(resultsDir, "experiments.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if err := json.NewEncoder(file).Encode(experiment); err != nil {
		_ = file.Close()
		return fmt.Errorf("append %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

// runExperiment runs each point until a run fails.
func runExperiment(ctx context.Context, env *cli.Env, experiment model.Experiment, dir string) error {
	for _, point := range experiment.Points() {
		if err := runDesignPoint(ctx, env, experiment.Run, dir, point); err != nil {
			return err
		}
	}
	return nil
}

func runDesignPoint(ctx context.Context, env *cli.Env, script model.Script, dir string, point model.Point) error {
	// Avoid starting a shell when the experiment is already canceled.
	if err := ctx.Err(); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "/bin/sh", "-c", expandRunScript(script, point))
	command.Dir = dir
	command.Stdin = env.Stdin
	command.Stderr = env.Stderr
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("run %v: %w", point, err)
	}

	// Reject conflicts so script output cannot silently replace inputs.
	result, err := decodeRunOutput(output, point)
	if err != nil {
		return err
	}
	for factor, setting := range point {
		if _, exists := result[string(factor)]; exists {
			return fmt.Errorf("run output conflicts with factor %q", factor)
		}
		result[string(factor)] = setting
	}
	return json.NewEncoder(env.Stdout).Encode(result)
}

func decodeRunOutput(output []byte, point model.Point) (map[string]any, error) {
	// Reject trailing values so each run contributes exactly one JSONL result.
	var result map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	if err := decoder.Decode(&result); err != nil || result == nil {
		return nil, fmt.Errorf("run %v: output must be a JSON object: %v", point, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("run %v: output must contain exactly one JSON object", point)
	}
	return result, nil
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
  experiment  Run an experiment and print JSON results.

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
