package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadJSONLRecoversOnlyInvalidUnterminatedFinalRecord(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantRecords int
		wantContent string
		wantError   string
	}{
		{
			name:        "partial final record",
			content:     "{\"id\":1}\n{\"id\":",
			wantRecords: 1,
			wantContent: "{\"id\":1}\n",
		},
		{
			name:        "valid final record without newline",
			content:     "{\"id\":1}\n{\"id\":2}",
			wantRecords: 2,
			wantContent: "{\"id\":1}\n{\"id\":2}",
		},
		{
			name:      "malformed terminated record",
			content:   "{\"id\":1}\n{\"id\":}\n",
			wantError: ":2:",
		},
		{
			name:      "trailing content",
			content:   "{\"id\":1} garbage",
			wantError: ":1:",
		},
		{
			name:      "second JSON value",
			content:   "{\"id\":1} {\"id\":2}",
			wantError: "exactly one JSON value",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "records.jsonl")
			writeFile(t, path, test.content)
			records := 0
			err := readJSONL(path, func([]byte) error {
				records++
				return nil
			})
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("readJSONL() error = %v, want containing %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if records != test.wantRecords {
				t.Fatalf("records = %d, want %d", records, test.wantRecords)
			}
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(content) != test.wantContent {
				t.Fatalf("content = %q, want %q", content, test.wantContent)
			}
		})
	}
}

func TestAppendJSONCreatesRecordBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.jsonl")
	writeFile(t, path, `{"id":1}`)
	if err := appendJSON(path, map[string]int{"id": 2}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "{\"id\":1}\n{\"id\":2}\n"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestExecuteRecoversPartialExperimentAndRunRecords(t *testing.T) {
	root := t.TempDir()
	designPath := filepath.Join(root, "design.yaml")
	writeFile(t, designPath, "factors: [{value: [x]}]\nrun: echo '{\"result\":1}'\n")
	if err := Execute(context.Background(), testEnv(new(bytes.Buffer), new(bytes.Buffer)), Options{Designs: []string{designPath}}); err != nil {
		t.Fatal(err)
	}

	output := filepath.Join(root, "results")
	for _, name := range []string{"experiments.jsonl", "runs.jsonl"} {
		path := filepath.Join(output, name)
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString(`{"interrupted":`); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}

	stderr := new(bytes.Buffer)
	if err := Execute(context.Background(), testEnv(new(bytes.Buffer), stderr), Options{Designs: []string{designPath}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr.String(), "result") {
		t.Fatalf("resume unexpectedly reran completed run: %s", stderr)
	}
	if got := lineCount(t, filepath.Join(output, "experiments.jsonl")); got != 2 {
		t.Fatalf("experiment count = %d, want 2", got)
	}
	if got := lineCount(t, filepath.Join(output, "runs.jsonl")); got != 1 {
		t.Fatalf("run count = %d, want 1", got)
	}
}
