package main

import (
	"context"
	"github.com/felixge/doe2/internal/cli"
	"github.com/felixge/doe2/internal/cmd"
	"os"
)

func main() {
	os.Exit(cmd.Main(context.Background(), cli.NewEnv(), os.Args[1:]))
}
