package jsonl

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.jsonl")
	if err := AppendFile(path, map[string]any{"value": 1}); err != nil {
		t.Fatal(err)
	}
	if err := AppendFile(path, map[string]any{"value": 2}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\"value\":1}\n{\"value\":2}\n"; string(data) != want {
		t.Errorf("file = %q, want %q", data, want)
	}
}

func TestAppendFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "records.jsonl")
	if err := AppendFile(path, 1); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), path) {
		t.Errorf("AppendFile missing directory = %v, want path and os.ErrNotExist", err)
	}

	path = filepath.Join(t.TempDir(), "records.jsonl")
	if err := AppendFile(path, make(chan int)); err == nil || !strings.Contains(err.Error(), "append "+path) {
		t.Errorf("AppendFile unsupported record = %v, want append error", err)
	}
}
