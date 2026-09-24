package main

import (
	"context"
	"github.com/felixge/doe/internal/cli"
	"github.com/felixge/doe/internal/cmd"
	"os"
)

func main() {
	os.Exit(cmd.Main(context.Background(), cli.NewEnv(), os.Args[1:]))
}
