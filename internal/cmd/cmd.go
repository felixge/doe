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
	"slices"
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
		return fail(env.Stderr, err)
	}

	var s model.Study
	if *file != "" {
		if err := s.Load(*file); err != nil {
			return fail(env.Stderr, err)
		}
	}
	experiment := model.NewExperiment(s)
	for _, arg := range flags.Args() {
		name, value, ok := strings.Cut(arg, "=")
		if !ok {
			return fail(env.Stderr, fmt.Errorf("factor %q must be key=value", arg))
		}
		if err := experiment.Factors.Set(model.Factor(name), []byte(value)); err != nil {
			return fail(env.Stderr, err)
		}
	}
	if flags.Changed("run") {
		experiment.Run = model.Script(*runScript)
	}
	if len(experiment.Factors) == 0 {
		return fail(env.Stderr, errors.New("at least one factor is required"))
	}
	if strings.TrimSpace(string(experiment.Run)) == "" {
		return fail(env.Stderr, errors.New("a run script is required (use --run or a study file)"))
	}
	dir := "."
	if *file != "" {
		dir = filepath.Dir(*file)
	}
	if err := runExperiment(ctx, env, experiment, dir); err != nil {
		if ctx.Err() != nil {
			return 130
		}
		return fail(env.Stderr, err)
	}
	return 0
}

// runExperiment sorts factor names for deterministic design points.
func runExperiment(ctx context.Context, env *cli.Env, experiment model.Experiment, dir string) error {
	names := make([]model.Factor, 0, len(experiment.Factors))
	for name := range experiment.Factors {
		names = append(names, name)
	}
	slices.Sort(names)
	values := make(map[model.Factor]model.Setting, len(names))
	var visit func(int) error
	visit = func(index int) error {
		if index == len(names) {
			if err := ctx.Err(); err != nil {
				return err
			}
			command := exec.CommandContext(ctx, "/bin/sh", "-c", expandRunScript(experiment.Run, values))
			command.Dir = dir
			command.Stdin = env.Stdin
			command.Stderr = env.Stderr
			output, err := command.Output()
			if err != nil {
				return fmt.Errorf("run %v: %w", values, err)
			}
			var result map[string]any
			decoder := json.NewDecoder(strings.NewReader(string(output)))
			if err := decoder.Decode(&result); err != nil || result == nil {
				return fmt.Errorf("run %v: output must be a JSON object: %v", values, err)
			}
			var extra any
			if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
				return fmt.Errorf("run %v: output must contain exactly one JSON object", values)
			}
			for _, name := range names {
				if _, exists := result[string(name)]; exists {
					return fmt.Errorf("run output conflicts with factor %q", name)
				}
				result[string(name)] = values[name]
			}
			return json.NewEncoder(env.Stdout).Encode(result)
		}
		name := names[index]
		for _, setting := range experiment.Factors[name] {
			values[name] = setting
			if err := visit(index + 1); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(0)
}

func expandRunScript(script model.Script, values map[model.Factor]model.Setting) string {
	return factorPlaceholder.ReplaceAllStringFunc(string(script), func(placeholder string) string {
		value, ok := values[model.Factor(placeholder[1:len(placeholder)-1])]
		if !ok {
			return placeholder
		}
		return fmt.Sprint(value)
	})
}

func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
	return 1
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

Factors are YAML values or sequences of values. CLI factors override file
factors. In run scripts, {factor} expands to the setting.
`)
}
