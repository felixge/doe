// Package cmd implements doe's command line interface.
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/felixge/doe/internal/cli"
	runcmd "github.com/felixge/doe/internal/cmd/run"
	"github.com/felixge/doe/internal/runner"
)

// Main executes the doe command.
func Main(ctx context.Context, env *cli.Env, args []string) int {
	return MainWithRun(ctx, env, args, runner.Execute)
}

// MainWithRun executes the doe command using execute for application-level run
// behavior. It is the integration boundary between CLI parsing and the study
// implementation.
func MainWithRun(ctx context.Context, env *cli.Env, args []string, execute runcmd.ExecuteFunc) int {
	ctx, stop := env.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
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
		return runcmd.Command(ctx, env, args[1:], execute)
	default:
		_, _ = fmt.Fprintf(env.Stderr, "unknown command: %s\n", args[0])
		return 1
	}
}

func rootUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `A lightweight CLI for design of experiments studies.

Usage: doe <command> [command options] [arguments]

Commands:
  run    Conduct or resume the execution of a study.

Run "doe <command> -h" for command-specific help.
`)
}
