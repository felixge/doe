// Package jsonl provides helpers for newline-delimited JSON files.
package jsonl

import (
	"encoding/json"
	"fmt"
	"os"
)

// AppendFile appends record as one JSON line to path, creating the file if needed.
func AppendFile(path string, record any) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if err := json.NewEncoder(file).Encode(record); err != nil {
		_ = file.Close()
		return fmt.Errorf("append %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}
