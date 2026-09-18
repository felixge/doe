package main

import (
	"context"
	_ "embed"
	"os"

	"github.com/felixge/doe/internal/cli"
	"github.com/felixge/doe/internal/cmd"
)

//go:embed README.md
var readme []byte

func main() {
	env := cli.NewEnv()
	env.Readme = readme
	code := cmd.Main(context.Background(), env, os.Args[1:])
	os.Exit(code)
}
