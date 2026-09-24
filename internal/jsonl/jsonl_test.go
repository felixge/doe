package jsonl

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestScanFile(t *testing.T) {
	type record struct {
		Value int `json:"value"`
	}
	path := filepath.Join(t.TempDir(), "records.jsonl")
	var got []record
	visit := func(value record) error {
		got = append(got, value)
		return nil
	}
	if err := ScanFile(path, visit); err != nil || len(got) != 0 {
		t.Fatalf("missing file = %v, %v; want empty", got, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("ScanFile created a missing file: %v", err)
	}
	if err := os.WriteFile(path, []byte("{\"value\":1}\n{\"value\":2}\n{\"value\":"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ScanFile(path, visit); err != nil || !reflect.DeepEqual(got, []record{{1}, {2}}) {
		t.Errorf("complete records = %v, %v; want values 1 and 2", got, err)
	}
	if err := os.WriteFile(path, []byte("bad JSON\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ScanFile(path, visit); err == nil || !strings.Contains(err.Error(), "decode "+path) {
		t.Errorf("malformed record error = %v", err)
	}
	failure := errors.New("visit failed")
	if err := os.WriteFile(path, []byte("{\"value\":1}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ScanFile(path, func(record) error { return failure }); !errors.Is(err, failure) {
		t.Errorf("visitor error = %v, want %v", err, failure)
	}
	var ptr *record
	if err := ScanFile(path, func(value *record) error { ptr = value; return nil }); err != nil || ptr == nil || ptr.Value != 1 {
		t.Errorf("pointer record = %+v, %v; want value 1", ptr, err)
	}
}

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
