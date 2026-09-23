package model

import (
	"fmt"
	"os"
	"strings"
)

// processStartTime returns the kernel's start tick, measured since boot.
// It can be compared with the same field for a PID without clock conversion.
func processStartTime() (string, error) {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return "", err
	}
	// The command name is parenthesized and may itself contain spaces or ')'.
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return "", fmt.Errorf("invalid /proc/self/stat")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) <= 19 {
		return "", fmt.Errorf("missing start time in /proc/self/stat")
	}
	return fields[19], nil // field 22, counting from the PID
}
