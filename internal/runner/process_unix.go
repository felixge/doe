//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package runner

import (
	"context"
	"os/exec"
	"syscall"
)

func configureProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func watchProcessGroup(ctx context.Context, command *exec.Cmd) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			killProcessGroup(command)
		case <-done:
		}
	}()
	return func() { close(done) }
}

func killProcessGroup(command *exec.Cmd) {
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
}
