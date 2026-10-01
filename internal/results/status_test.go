package results

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/felixge/doe/internal/model"
)

func createStatusExperiment(t *testing.T, r *Results, experiment *model.Experiment) {
	t.Helper()
	log, err := r.CreateExperiment(experiment)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
}

func createStatusRun(t *testing.T, r *Results, run *model.Run) {
	t.Helper()
	log, err := r.CreateRun(run)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.AppendRun(run.ExperimentID, run.ID); err != nil {
		t.Fatal(err)
	}
}

func TestStatusTransitionsAndActualRun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "results")
	r := Open(dir)
	if status, err := r.Status(); err != nil || status != nil {
		t.Fatalf("missing directory = %+v, %v; want no status", status, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("Status created results directory: %v", err)
	}
	r, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	experiment := model.NewExperiment(model.Design{
		Factors: model.Factors{"foo": {1, 2}}, Run: "echo {foo}", Replicates: 1,
	})
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	experiment.Start = now.Add(-2 * time.Minute)
	release, err := r.Lock(experiment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()

	createStatusExperiment(t, r, &experiment)
	status, err := r.statusAt(now)
	if err != nil || status.State != StateSetup || status.Total != 2 || status.Completed != 0 || status.ExperimentElapsed != 2*time.Minute {
		t.Fatalf("setup status = %+v, %v", status, err)
	}

	experiment.State = model.StateRunning
	experiment.SetupEnd = now.Add(-time.Minute)
	if err := r.UpdateExperiment(&experiment); err != nil {
		t.Fatal(err)
	}
	active := model.NewRun(experiment.ID, experiment.Points[1], 7)
	active.Start = now.Add(-3 * time.Second)
	createStatusRun(t, r, active)
	status, err = r.statusAt(now)
	if err != nil || status.State != StateRunning || status.RunReplicate != 7 ||
		status.RunPoint.String() != "foo=2" || status.RunElapsed != 3*time.Second {
		t.Fatalf("running status = %+v, %v; want actual active run", status, err)
	}

	active.State = model.StateDone
	active.End = now
	active.Outcome = model.Outcome{"ok": true}
	if err := r.UpdateRun(active); err != nil {
		t.Fatal(err)
	}
	experiment.State = model.StateDone
	experiment.End = now
	if err := r.UpdateExperiment(&experiment); err != nil {
		t.Fatal(err)
	}
	status, err = r.statusAt(now.Add(time.Hour))
	if err != nil || status.State != StateDone || status.Completed != 1 || status.ExperimentElapsed != 2*time.Minute || status.RunReplicate != 0 {
		t.Errorf("done status = %+v, %v", status, err)
	}
}

func TestStatusCountsOnlySuccessfulTerminalRuns(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	experiment := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {1, 2, 3}}, Replicates: 1})
	release, err := r.Lock(experiment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	createStatusExperiment(t, r, &experiment)
	experiment.State = model.StateRunning
	if err := r.UpdateExperiment(&experiment); err != nil {
		t.Fatal(err)
	}
	states := []model.State{model.StateDone, model.StateStopped, model.StateError}
	for index, state := range states {
		run := model.NewRun(experiment.ID, experiment.Points[index], 1)
		run.State = state
		run.End = run.Start
		if state == model.StateError {
			run.Error = "exit status 7"
		}
		createStatusRun(t, r, run)
	}
	status, err := r.Status()
	if err != nil || status.Completed != 1 || status.State != StateError || status.Error != "exit status 7" {
		t.Errorf("mixed run status = %+v, %v", status, err)
	}
}

func TestStatusUnfinishedWithoutOwnerIsStopped(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	experiment := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {1}}, Replicates: 1})
	release, err := r.Lock(experiment.ID)
	if err != nil {
		t.Fatal(err)
	}
	createStatusExperiment(t, r, &experiment)
	experiment.State = model.StateRunning
	if err := r.UpdateExperiment(&experiment); err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	status, err := r.Status()
	if err != nil || status.State != StateStopped || status.ExperimentElapsed != 0 {
		t.Errorf("orphaned experiment status = %+v, %v", status, err)
	}
}

func TestStatusRemainingEstimate(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	experiment := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {1, 2, 3}}, Replicates: 2})
	experiment.State = model.StateRunning
	release, err := r.Lock(experiment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	createStatusExperiment(t, r, &experiment)
	started := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	first := model.NewRun(experiment.ID, experiment.Points[0], 1)
	first.Start, first.End, first.State = started, started.Add(2*time.Second), model.StateDone
	createStatusRun(t, r, first)
	active := model.NewRun(experiment.ID, experiment.Points[1], 1)
	active.Start = started.Add(2 * time.Second)
	createStatusRun(t, r, active)
	status, err := r.statusAt(started.Add(3 * time.Second))
	// Six planned runs at 2s each, with one complete and 1s spent on the active run.
	if err != nil || status.Remaining != 9*time.Second {
		t.Errorf("estimate = %+v, %v; want 9s", status, err)
	}
}

func TestStatusRemainingUsesPointAverages(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	experiment := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {1, 2}}, Replicates: 2})
	experiment.State = model.StateRunning
	release, err := r.Lock(experiment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	createStatusExperiment(t, r, &experiment)
	started := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	first := model.NewRun(experiment.ID, experiment.Points[0], 1)
	first.Start, first.End, first.State = started, started.Add(2*time.Second), model.StateDone
	createStatusRun(t, r, first)
	second := model.NewRun(experiment.ID, experiment.Points[1], 1)
	second.Start, second.End, second.State = first.End, first.End.Add(6*time.Second), model.StateDone
	createStatusRun(t, r, second)
	// The second replicate reverses the point order: Points[1], then Points[0].
	active := model.NewRun(experiment.ID, experiment.Points[1], 2)
	active.Start = second.End
	createStatusRun(t, r, active)
	status, err := r.statusAt(active.Start.Add(time.Second))
	if err != nil || status.Completed != 2 || status.Remaining != 7*time.Second {
		t.Errorf("status = %+v, %v; want 2 completed and 7s remaining", status, err)
	}
}

func TestStatusInvalidLock(t *testing.T) {
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
