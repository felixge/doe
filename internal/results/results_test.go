package results

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/felixge/doe2/internal/model"
)

func TestAppendExperiment(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "results")
	results, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("New did not create results directory: %v", err)
	}
	first := model.NewExperiment(model.Study{})
	second := model.NewExperiment(model.Study{})
	for _, experiment := range []model.Experiment{first, second} {
		if err := results.AppendExperiment(experiment); err != nil {
			t.Fatal(err)
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, "experiments.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d records, want 2", len(lines))
	}
	for i, want := range []model.Experiment{first, second} {
		var got model.Experiment
		if err := json.Unmarshal([]byte(lines[i]), &got); err != nil {
			t.Fatal(err)
		}
		if got.ID != want.ID {
			t.Errorf("record %d ID = %s, want %s", i, got.ID, want.ID)
		}
	}
}

func TestAppendExperimentErrors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "results")
	if err := os.WriteFile(dir, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir); err == nil || !strings.Contains(err.Error(), "create results directory") {
		t.Errorf("New with invalid directory = %v, want directory error", err)
	}

	dir = t.TempDir()
	results, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "experiments.jsonl")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := results.AppendExperiment(model.Experiment{}); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("AppendExperiment with invalid file = %v, want path error", err)
	}
}
