package main

import (
	"context"
	_ "embed"
	"os"
	"os/signal"
	"syscall"

	"github.com/felixge/doe/internal/cli"
	"github.com/felixge/doe/internal/cmd"
)

//go:embed README.md
var readme []byte

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	env := cli.NewEnv(readme)
	code := cmd.Main(ctx, env, os.Args[1:])
	stop()
	os.Exit(code)
}
