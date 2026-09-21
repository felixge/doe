// Package cli provides process resources shared by doe commands.
package cli

import (
	"io"
	"os"
)

// Env contains the process resources used by doe commands.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Readme []byte
}

// NewEnv returns an environment connected to the current process.
func NewEnv(readme []byte) *Env {
	return &Env{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Readme: readme,
	}
}
