// Package cli provides process resources shared by doe commands.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// Env contains the process resources used by doe commands.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Fail writes an error to stderr and returns a failure exit code.
// Context cancellation returns 130 without writing an error.
func (e *Env) Fail(err error) int {
	if errors.Is(err, context.Canceled) {
		return 130
	}
	_, _ = fmt.Fprintf(e.Stderr, "error: %v\n", err)
	return 1
}

// NewEnv returns an environment connected to the current process.
func NewEnv() *Env {
	return &Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
}
