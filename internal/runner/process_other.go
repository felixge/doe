//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package runner

import "os/exec"

func configureProcessGroup(_ *exec.Cmd) {}
