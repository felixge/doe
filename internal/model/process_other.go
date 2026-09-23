//go:build !darwin && !linux

package model

import "errors"

func processStartTime() (string, error) {
	return "", errors.New("process start time is supported only on macOS and Linux")
}
