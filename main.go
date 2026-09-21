package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/felixge/doe/internal/cli"
	"github.com/felixge/doe/internal/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	env := cli.NewEnv()
	code := cmd.Main(ctx, env, os.Args[1:])
	stop()
	os.Exit(code)
}
