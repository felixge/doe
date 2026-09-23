package model

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// processStartTime returns the kernel's process creation timestamp.
func processStartTime() (string, error) {
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", os.Getpid())
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%06d", proc.Proc.P_starttime.Sec, proc.Proc.P_starttime.Usec), nil
}
