// Package jsonl provides helpers for newline-delimited JSON files.
package jsonl

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// ScanFile visits each complete JSON line in path. A missing file is empty,
// and an unterminated final line is ignored if a writer was interrupted.
func ScanFile[T any](path string, visit func(T) error) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(file)
	for {
		data, err := reader.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		var record T
		if err := json.Unmarshal(bytes.TrimSpace(data), &record); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
		if err := visit(record); err != nil {
			return fmt.Errorf("visit %s: %w", path, err)
		}
	}
}

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
