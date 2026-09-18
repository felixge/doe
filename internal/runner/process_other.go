//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package runner

import (
	"context"
	"os"
	"os/exec"
)

func configureProcessGroup(_ *exec.Cmd) {}

func watchProcessGroup(_ context.Context, _ *exec.Cmd) func() {
	return func() {}
}

func killProcessGroup(command *exec.Cmd) { _ = command.Process.Kill() }

func lockFile(_ *os.File) error   { return nil }
func unlockFile(_ *os.File) error { return nil }
