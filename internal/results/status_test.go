package results

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/felixge/doe2/internal/model"
)

func TestReadStatusTransitions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "results")
	r := Open(dir)
	if status, err := r.Status(); err != nil || status != nil {
		t.Fatalf("missing directory = %+v, %v; want no status", status, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("ReadStatus created results directory: %v", err)
	}
	r, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if status, err := r.Status(); err != nil || status != nil {
		t.Fatalf("empty directory = %+v, %v; want no status", status, err)
	}
	experiment := model.NewExperiment(model.Design{
		Factors: model.Factors{"foo": {1, 2}}, Run: "echo '{}'", Replicates: 1,
	})
	check := func(state State, completed, total int, errorText string) {
		t.Helper()
		status, err := r.Status()
		if err != nil {
			t.Fatal(err)
		}
		want := &Status{ExperimentID: experiment.ID, State: state, Completed: completed, Total: total, Error: errorText}
		if !reflect.DeepEqual(status, want) {
			t.Errorf("status = %+v, want %+v", status, want)
		}
	}
	release, err := r.LockExperiment(experiment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	check(StateSetup, 0, 0, "")
	if err := r.AppendExperiment(&experiment); err != nil {
		t.Fatal(err)
	}
	check(StateRunning, 0, 2, "")
	if err := r.AppendRun(model.NewRun(experiment.ID, experiment.Points[0], 1)); err != nil {
		t.Fatal(err)
	}
	check(StateRunning, 1, 2, "")
	failed := model.NewRun(experiment.ID, experiment.Points[1], 1)
	failed.Error = "exit status 7"
	if err := r.AppendRun(failed); err != nil {
		t.Fatal(err)
	}
	check(StateError, 1, 2, "exit status 7")
	if err := release(); err != nil {
		t.Fatal(err)
	}
	check(StateError, 1, 2, "exit status 7")
}

func TestReadStatusStoppedAndDone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		state State
	}{
		{"no runs", 0, StateStopped},
		{"one run", 1, StateStopped},
		{"all runs", 2, StateDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			experiment := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {1, 2}}, Replicates: 1})
			if err := r.AppendExperiment(&experiment); err != nil {
				t.Fatal(err)
			}
			for _, point := range experiment.Points[:tc.count] {
				if err := r.AppendRun(model.NewRun(experiment.ID, point, 1)); err != nil {
					t.Fatal(err)
				}
			}
			status, err := r.Status()
			if err != nil || status == nil {
				t.Fatalf("ReadStatus = %+v, %v", status, err)
			}
			if status.ExperimentID != experiment.ID || status.State != tc.state || status.Completed != tc.count || status.Total != 2 {
				t.Errorf("status = %+v, want %s with %d/2 runs", status, tc.state, tc.count)
			}
		})
	}
}

func TestReadStatusSetupFailureAndStoppedSetup(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	experiment := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {1}}, Replicates: 1})
	release, err := r.LockExperiment(experiment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	status, err := r.Status()
	if err != nil || status == nil || status.State != StateStopped || status.ExperimentID != experiment.ID || status.Total != 0 {
		t.Fatalf("stopped before experiment record = %+v, %v", status, err)
	}
	experiment.SetupError = "setup: exit status 7"
	if err := r.AppendExperiment(&experiment); err != nil {
		t.Fatal(err)
	}
	status, err = r.Status()
	if err != nil || status == nil || status.State != StateError || status.Error != experiment.SetupError || status.Completed != 0 || status.Total != 1 {
		t.Errorf("failed setup = %+v, %v", status, err)
	}
}

func TestReadStatusLatestExperiment(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {1}}, Replicates: 1})
	second := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {2}}, Replicates: 1})
	for _, experiment := range []*model.Experiment{&first, &second} {
		if err := r.AppendExperiment(experiment); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.AppendRun(model.NewRun(first.ID, first.Points[0], 1)); err != nil {
		t.Fatal(err)
	}
	status, err := r.Status()
	if err != nil || status == nil || status.ExperimentID != second.ID || status.Completed != 0 || status.State != StateStopped {
		t.Errorf("latest experiment without lock = %+v, %v", status, err)
	}
	release, err := r.LockExperiment(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	status, err = r.Status()
	if err != nil || status == nil || status.ExperimentID != first.ID || status.Completed != 1 || status.State != StateRunning {
		t.Errorf("locked experiment = %+v, %v", status, err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	third := model.NewExperiment(model.Design{})
	releaseThird, err := r.LockExperiment(third.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = releaseThird() }()
	status, err = r.Status()
	if err != nil || status == nil || status.ExperimentID != third.ID || status.State != StateSetup || status.Total != 0 {
		t.Errorf("new setup with older records = %+v, %v", status, err)
	}
}

func TestReadStatusInvalidLock(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.lockPath(), []byte("not an ID\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Status(); err == nil || !strings.Contains(err.Error(), "read experiment ID") {
		t.Errorf("invalid lock error = %v", err)
	}
}

func TestReadStatusPartialAndCorruptRecords(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	experiment := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {1}}, Replicates: 1})
	release, err := r.LockExperiment(experiment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	if err := r.AppendExperiment(&experiment); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.Dir(), "runs.jsonl")
	if err := os.WriteFile(path, []byte(`{"experiment_id":`), 0600); err != nil {
		t.Fatal(err)
	}
	status, err := r.Status()
	if err != nil || status == nil || status.State != StateRunning {
		t.Errorf("partial active record = %+v, %v", status, err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	status, err = r.Status()
	if err != nil || status == nil || status.State != StateStopped || status.Completed != 0 {
		t.Errorf("partial stopped record = %+v, %v", status, err)
	}
	if err := os.WriteFile(path, []byte("not JSON\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Status(); err == nil || !strings.Contains(err.Error(), "decode ") {
		t.Errorf("invalid record error = %v", err)
	}
}
