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
	"strings"
	"syscall"

	"github.com/felixge/doe2/internal/cli"
	"github.com/felixge/doe2/internal/study"
	"github.com/spf13/pflag"
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
		return run(ctx, env, args[1:])
	default:
		_, _ = fmt.Fprintf(env.Stderr, "unknown command: %s\n", args[0])
		return 1
	}
}

func run(ctx context.Context, env *cli.Env, args []string) int {
	flags := pflag.NewFlagSet("doe run", pflag.ContinueOnError)
	flags.SetOutput(env.Stderr)
	flags.SetInterspersed(true)
	flags.Usage = func() {}
	file := flags.StringP("file", "f", "", "study YAML file")
	runScript := flags.StringP("run", "r", "", "shell script to run at each design point")
	if err := flags.Parse(args); errors.Is(err, pflag.ErrHelp) {
		runUsage(env.Stdout)
		return 0
	} else if err != nil {
		return fail(env.Stderr, err)
	}

	var s study.Study
	if *file != "" {
		var err error
		s, err = study.Load(*file)
		if err != nil {
			return fail(env.Stderr, err)
		}
	}
	for _, arg := range flags.Args() {
		name, value, ok := strings.Cut(arg, "=")
		if !ok {
			return fail(env.Stderr, fmt.Errorf("factor %q must be key=value", arg))
		}
		if err := s.Factors.Set(name, value); err != nil {
			return fail(env.Stderr, err)
		}
	}
	if flags.Changed("run") {
		s.Run = *runScript
	}
	if len(s.Factors.Names) == 0 {
		return fail(env.Stderr, errors.New("at least one factor is required"))
	}
	if strings.TrimSpace(s.Run) == "" {
		return fail(env.Stderr, errors.New("a run script is required (use --run or a study file)"))
	}
	dir := "."
	if *file != "" {
		dir = filepath.Dir(*file)
	}
	if err := execute(ctx, env, s, dir); err != nil {
		if ctx.Err() != nil {
			return 130
		}
		return fail(env.Stderr, err)
	}
	return 0
}

// execute visits the first factor fastest, matching the README's sum example.
func execute(ctx context.Context, env *cli.Env, s study.Study, dir string) error {
	values := make(map[string]string, len(s.Factors.Names))
	var visit func(int) error
	visit = func(index int) error {
		if index < 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			command := exec.CommandContext(ctx, "/bin/sh", "-c", s.Run)
			command.Dir = dir
			command.Stdin = env.Stdin
			command.Stderr = env.Stderr
			command.Env = os.Environ()
			for _, name := range s.Factors.Names {
				command.Env = append(command.Env, name+"="+values[name])
			}
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
			for _, name := range s.Factors.Names {
				if _, exists := result[name]; exists {
					return fmt.Errorf("run output conflicts with factor %q", name)
				}
				result[name] = values[name]
			}
			return json.NewEncoder(env.Stdout).Encode(result)
		}
		name := s.Factors.Names[index]
		for _, setting := range s.Factors.Settings[name] {
			values[name] = setting
			if err := visit(index - 1); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(len(s.Factors.Names) - 1)
}

func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
	return 1
}

func rootUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `A lightweight CLI for design of experiments studies.

Usage: doe <command> [command options] [arguments]

Commands:
  run    Execute a study and print JSON results.

Run "doe <command> -h" for command-specific help.
`)
}

func runUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Execute one run for each combination of factor settings.

Usage: doe run [-f study.yaml] [key=value ...] [-r script]

Options:
  -f, --file  Load factors and run script from a YAML study.
  -r, --run   Override the run script with a shell command.
  -h, --help  Print help text.

Factors are YAML scalars or sequences of scalars. CLI factors override file
factors. Each run must emit one JSON object; combined input and output objects
are printed as JSON lines. No results are saved yet.
`)
}
