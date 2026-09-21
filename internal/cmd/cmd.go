// Package cmd implements doe's command line interface.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/felixge/doe/internal/cli"
	"github.com/felixge/doe/internal/runner"
	"github.com/spf13/pflag"
)

// Main executes the doe command.
func Main(ctx context.Context, env *cli.Env, args []string) int {
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
	opts, help, err := parseRun(env.Stderr, args)
	if help {
		runUsage(env.Stdout)
		return 0
	}
	if err != nil {
		return fail(env.Stderr, err)
	}
	if err := runner.Execute(ctx, env, opts); err != nil {
		return fail(env.Stderr, err)
	}
	return 0
}

func parseRun(stderr io.Writer, args []string) (runner.Options, bool, error) {
	var opts runner.Options
	flags := pflag.NewFlagSet("doe run", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.SetInterspersed(true)
	flags.Usage = func() {}
	flags.BoolVarP(&opts.Plan, "plan", "p", false, "show design points and their schedule without running them")
	flags.BoolVarP(&opts.Setup, "setup", "s", false, "run setup without running experiments")
	flags.BoolVarP(&opts.Dirty, "dirty", "d", false, "run even if this will dirty the results")
	flags.BoolVarP(&opts.Clean, "clean", "c", false, "remove the results and work directories before running")

	err := flags.Parse(args)
	if errors.Is(err, pflag.ErrHelp) {
		return runner.Options{}, true, nil
	}
	if err != nil {
		return runner.Options{}, false, err
	}
	opts.Designs = append([]string(nil), flags.Args()...)
	if len(opts.Designs) == 0 {
		return runner.Options{}, false, errors.New("at least one design is required")
	}
	return opts, false, nil
}

func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
	return 1
}

func rootUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `A lightweight CLI for design of experiments studies.

Usage: doe <command> [command options] [arguments]

Commands:
  run    Conduct or resume the execution of a study.

Run "doe <command> -h" for command-specific help.
`)
}

func runUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Run performs one experiment per design. Previous runs are reused, allowing work to be resumed.

Usage: doe run [options] <design>...

Arguments:
  <design>...           Paths to one or more design YAML files

Options:
  -p, --plan            Show the design points and schedule. Do not run them.
  -s, --setup           Run setup without running experiments.
  -d, --dirty           Run the study even if it will dirty the results.
  -c, --clean           Remove the results and work directories before running.
  -h, --help            Print help text.

Examples:
  # Run a design
  doe run design.yaml
  # Run a design, even if it will produce dirty results
  doe run -d design.yaml
  # Remove previous results and work before running a design
  doe run -c design.yaml
  # Show the plan for the design
  doe run -p design.yaml
  # Run setup without running experiments
  doe run -s design.yaml
`)
}
