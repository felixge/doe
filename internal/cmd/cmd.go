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
	flags.BoolVarP(&opts.Dirty, "dirty", "d", false, "run even if this will dirty the results")

	err := flags.Parse(args)
	if errors.Is(err, pflag.ErrHelp) {
		return runner.Options{}, true, nil
	}
	if err != nil {
		return runner.Options{}, false, err
	}
	if flags.NArg() == 0 {
		return runner.Options{}, false, errors.New("a project directory is required")
	}
	opts.Project = flags.Arg(0)
	opts.Designs = append([]string(nil), flags.Args()[1:]...)
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
	_, _ = fmt.Fprint(w, `Run performs one experiment per selected study. Completed runs are reused across designs.

Usage: doe run [options] <project-directory> <design>...

Arguments:
  <project-directory>  Directory containing top-level *.study.yaml files
  <design>...           Qualified study/design selectors or unique short names

Options:
  -p, --plan            Show the design points and schedule. Do not run them.
  -d, --dirty           Run the study even if it will dirty the results.
  -h, --help            Print help text.

Examples:
  doe run . compression/smoke compression/full
  doe run . smoke full
  doe run --dirty . full
  doe run --plan . full

Designs execute in argument order. Setup runs once per study, before its first
selected design. Results are stored under results/<study>/. Normal runs leave
stdout empty; progress and diagnostics go to stderr.
`)
}
