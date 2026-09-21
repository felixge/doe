//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package runner

import (
	"context"
	"os/exec"
)

func configureProcessGroup(_ *exec.Cmd) {}

func watchProcessGroup(_ context.Context, _ *exec.Cmd) func() {
	return func() {}
}

func killProcessGroup(command *exec.Cmd) { _ = command.Process.Kill() }
