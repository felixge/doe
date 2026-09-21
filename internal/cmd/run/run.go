// Package run implements the doe run command.
package run

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/felixge/doe/internal/cli"
	"github.com/spf13/pflag"
)

// Options contains the validated command-line arguments for doe run.
type Options struct {
	Designs []string
	Plan    bool
	Dirty   bool
}

// ExecuteFunc conducts a run after its command-line arguments are parsed.
type ExecuteFunc func(context.Context, *cli.Env, Options) error

// Command parses and executes doe run.
func Command(ctx context.Context, env *cli.Env, args []string, execute ExecuteFunc) int {
	opts, help, err := parse(env.Stderr, args)
	if help {
		runUsage(env.Stdout)
		return 0
	}
	if err != nil {
		return env.Fail(err)
	}
	if execute == nil {
		return env.Fail(errors.New("run execution is not configured"))
	}
	if err := execute(ctx, env, opts); err != nil {
		return env.Fail(err)
	}
	return 0
}

func parse(stderr io.Writer, args []string) (Options, bool, error) {
	var opts Options
	flags := pflag.NewFlagSet("doe run", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.SetInterspersed(true)
	// Command prints help to stdout after Parse reports ErrHelp. Keeping the
	// flag set's callback empty avoids pflag also printing it to stderr.
	flags.Usage = func() {}
	flags.BoolVarP(&opts.Plan, "plan", "p", false, "show design points and their schedule without running them")
	flags.BoolVarP(&opts.Dirty, "dirty", "d", false, "run even if this will dirty the results")

	err := flags.Parse(args)
	if errors.Is(err, pflag.ErrHelp) {
		return Options{}, true, nil
	}
	if err != nil {
		return Options{}, false, err
	}
	opts.Designs = append([]string(nil), flags.Args()...)
	if len(opts.Designs) == 0 {
		return Options{}, false, errors.New("at least one design is required")
	}
	return opts, false, nil
}

func runUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Run performs one experiment per design. Previous runs are reused, allowing work to be resumed.

Usage: doe run [options] <design>...

Arguments:
  <design>...           Paths to one or more design YAML files

Options:
  -p, --plan            Show the design points and schedule. Do not run them.
  -d, --dirty           Run the study even if it will dirty the results.
  -h, --help            Print help text.

Examples:
  # Run a design
  doe run design.yaml
  # Run a design, even if it will produce dirty results
  doe run -d design.yaml
  # Show the plan for the design
  doe run -p design.yaml
`)
}
