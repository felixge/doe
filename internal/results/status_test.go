package results

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	experiment.Start = now.Add(-2 * time.Minute)
	check := func(state State, completed, total int, errorText string) {
		t.Helper()
		status, err := r.statusAt(now)
		if err != nil {
			t.Fatal(err)
		}
		want := &Status{ExperimentID: experiment.ID, State: state, Completed: completed, Total: total, Error: errorText}
		if state == StateRunning {
			want.ExperimentElapsed = 2 * time.Minute
		}
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

func TestStatusElapsedTimes(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	experiment := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {1, 2}}, Replicates: 1})
	started := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	experiment.Start = started
	release, err := r.LockExperiment(experiment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	if err := r.AppendExperiment(&experiment); err != nil {
		t.Fatal(err)
	}
	check := func(now time.Time, state State, experimentElapsed time.Duration, runElapsed bool, runTime time.Duration) {
		t.Helper()
		status, err := r.statusAt(now)
		if err != nil {
			t.Fatal(err)
		}
		if status.State != state || status.ExperimentElapsed != experimentElapsed || (status.RunElapsed > 0) != runElapsed || status.RunElapsed != runTime {
			t.Errorf("status at %s = %+v; want %s, experiment %s, run %s (available %t)", now, status, state, experimentElapsed, runTime, runElapsed)
		}
	}
	check(started.Add(time.Second), StateRunning, time.Second, false, 0)
	first := model.NewRun(experiment.ID, experiment.Points[0], 1)
	first.Start, first.End = started, started.Add(5*time.Second)
	if err := r.AppendRun(first); err != nil {
		t.Fatal(err)
	}
	check(started.Add(8*time.Second), StateRunning, 8*time.Second, true, 3*time.Second)
	second := model.NewRun(experiment.ID, experiment.Points[1], 1)
	second.Start, second.End = first.End, started.Add(10*time.Second)
	if err := r.AppendRun(second); err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	check(started.Add(time.Hour), StateDone, 10*time.Second, false, 0)
}

func TestStatusEstimatesRemaining(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	experiment := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {1, 2, 3}}, Replicates: 2})
	release, err := r.LockExperiment(experiment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	if err := r.AppendExperiment(&experiment); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	check := func(now time.Time, want time.Duration, available bool) {
		t.Helper()
		status, err := r.statusAt(now)
		if err != nil {
			t.Fatal(err)
		}
		if status.State != StateRunning || (status.Remaining > 0) != available || status.Remaining != want {
			t.Errorf("status at %s = %+v; want remaining %s, available %t", now, status, want, available)
		}
	}
	check(started, 0, false)
	appendRun := func(point int, start, end time.Time) {
		t.Helper()
		run := model.NewRun(experiment.ID, experiment.Points[point], 1)
		run.Start, run.End = start, end
		if err := r.AppendRun(run); err != nil {
			t.Fatal(err)
		}
	}
	appendRun(0, started, started.Add(2*time.Second))
	// The remaining five runs all fall back to the sole 2s sample.
	check(started.Add(3*time.Second), 9*time.Second, true)
	appendRun(1, started.Add(2*time.Second), started.Add(12*time.Second))
	// The next point is unseen: 6s global average minus 1s elapsed.
	// The other points use their own averages (10s and 2s).
	check(started.Add(13*time.Second), 23*time.Second, true)
	check(started.Add(30*time.Second), 18*time.Second, true)
}

func TestStatusEstimateLegacyAndInactiveRuns(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	experiment := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {1, 2}}, Replicates: 2})
	release, err := r.LockExperiment(experiment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	if err := r.AppendExperiment(&experiment); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	legacy := model.NewRun(experiment.ID, experiment.Points[0], 1)
	legacy.Start = time.Time{}
	if err := r.AppendRun(legacy); err != nil {
		t.Fatal(err)
	}
	status, err := r.statusAt(started)
	if err != nil || status.Remaining != 0 {
		t.Fatalf("legacy-only status = %+v, %v; want no estimate", status, err)
	}
	run := model.NewRun(experiment.ID, experiment.Points[1], 1)
	run.Start, run.End = started, started.Add(10*time.Second)
	if err := r.AppendRun(run); err != nil {
		t.Fatal(err)
	}
	status, err = r.statusAt(started.Add(13 * time.Second))
	if err != nil || status.Remaining != 17*time.Second {
		t.Fatalf("legacy and timed status = %+v, %v; want 17s", status, err)
	}
	// A missing end time on the latest run disables the elapsed adjustment.
	legacy = model.NewRun(experiment.ID, experiment.Points[1], 2)
	legacy.End = time.Time{}
	if err := r.AppendRun(legacy); err != nil {
		t.Fatal(err)
	}
	status, err = r.statusAt(started.Add(13 * time.Second))
	if err != nil || status.Remaining != 10*time.Second {
		t.Fatalf("latest legacy run status = %+v, %v; want 10s", status, err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	status, err = r.statusAt(started.Add(13 * time.Second))
	if err != nil || status.State != StateStopped || status.Remaining != 0 {
		t.Errorf("stopped status = %+v, %v; want no estimate", status, err)
	}
}

func TestStatusEstimateIgnoresOtherExperiments(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {1}}, Replicates: 1})
	if err := r.AppendExperiment(&old); err != nil {
		t.Fatal(err)
	}
	run := model.NewRun(old.ID, old.Points[0], 1)
	run.End = run.Start.Add(10 * time.Second)
	if err := r.AppendRun(run); err != nil {
		t.Fatal(err)
	}
	current := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {1, 2}}, Replicates: 1})
	release, err := r.LockExperiment(current.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	if err := r.AppendExperiment(&current); err != nil {
		t.Fatal(err)
	}
	status, err := r.statusAt(run.End)
	if err != nil || status.State != StateRunning || status.Remaining != 0 {
		t.Errorf("new experiment status = %+v, %v; want no estimate", status, err)
	}
}

func TestStatusEstimateStopsOnErrorOrCompletion(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "done", true: "error"}[fail], func(t *testing.T) {
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
			run := model.NewRun(experiment.ID, experiment.Points[0], 1)
			run.End = run.Start.Add(time.Second)
			if fail {
				run.Error = "failed"
			}
			if err := r.AppendRun(run); err != nil {
				t.Fatal(err)
			}
			status, err := r.statusAt(run.End)
			if err != nil || status.Remaining != 0 {
				t.Errorf("finished status = %+v, %v; want no estimate", status, err)
			}
		})
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
