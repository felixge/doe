package results

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/felixge/doe/internal/model"
)

func TestExperimentLock(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := model.NewExperiment(model.Design{}).ID
	other := model.NewExperiment(model.Design{}).ID
	release, err := r.LockExperiment(id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	if otherRelease, err := r.LockExperiment(other); err == nil {
		_ = otherRelease()
		t.Error("second experiment acquired the lock")
	} else if !strings.Contains(err.Error(), "another experiment is running: "+id.String()) {
		t.Errorf("lock error = %q, want running experiment ID", err)
	}
	data, err := os.ReadFile(filepath.Join(r.Dir(), "experiment.lock"))
	if err != nil || strings.TrimSpace(string(data)) != id.String() {
		t.Errorf("lock contains %q, %v; want %s", data, err, id)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	releaseOther, err := r.LockExperiment(other)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = releaseOther() }()
	data, err = os.ReadFile(filepath.Join(r.Dir(), "experiment.lock"))
	if err != nil || strings.TrimSpace(string(data)) != other.String() {
		t.Errorf("lock contains %q, %v; want %s", data, err, other)
	}
}

func TestRunOutcomeAndAppendErrors(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := &model.Run{ID: model.NewExperiment(model.Design{}).ID, Point: model.Point{"foo": 1}}
	if _, err := r.RunOutcome(run.ID); err == nil || !strings.Contains(err.Error(), "open run log") {
		t.Errorf("missing log error = %v", err)
	}
	path := filepath.Join(r.Dir(), "runs.jsonl")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := r.AppendRun(run); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("AppendRun with invalid file = %v, want path error", err)
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
	if err := results.AppendExperiment(&model.Experiment{}); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("AppendExperiment with invalid file = %v, want path error", err)
	}
}
