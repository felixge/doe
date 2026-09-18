// Package cli contains the process resources used by the command line.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
)

// Env contains process resources that commands use. Tests can replace them.
type Env struct {
	Stdin         io.Reader
	Stdout        io.Writer
	Stderr        io.Writer
	Readme        []byte
	NotifyContext func(context.Context, ...os.Signal) (context.Context, context.CancelFunc)
}

// NewEnv returns an environment connected to the current process.
func NewEnv() *Env {
	return &Env{
		Stdin:         os.Stdin,
		Stdout:        os.Stdout,
		Stderr:        os.Stderr,
		NotifyContext: signal.NotifyContext,
	}
}

// Fail prints an error and returns the conventional failure exit code.
func (e *Env) Fail(err error) int {
	_, _ = fmt.Fprintf(e.Stderr, "error: %v\n", err)
	return 1
}
