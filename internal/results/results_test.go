package results

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/felixge/doe/internal/model"
	"uuid"
)

func TestLockAndClean(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewV7()
	other := uuid.NewV7()
	release, err := r.Lock(id)
	if err != nil {
		t.Fatal(err)
	}
	if otherRelease, err := r.Lock(other); err == nil {
		_ = otherRelease()
		t.Error("second experiment acquired the lock")
	} else if !strings.Contains(err.Error(), "another experiment is running: "+id.String()) {
		t.Errorf("lock error = %q, want running experiment ID", err)
	}
	lockPath := filepath.Join(dir, resultsLockFile)
	before, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.Clean(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("Clean replaced the lock file")
	}
	data, err := os.ReadFile(lockPath)
	if err != nil || strings.TrimSpace(string(data)) != id.String() {
		t.Errorf("lock contains %q, %v; want %s", data, err, id)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	releaseOther, err := r.Lock(other)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = releaseOther() }()
}

func TestExperimentAndRunRecords(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	experiment := model.NewExperiment(model.Design{
		Factors: model.Factors{"foo": {1}}, Run: "echo {foo}", Replicates: 1,
	})
	setupLog, err := r.CreateExperiment(&experiment)
	if err != nil {
		t.Fatal(err)
	}
	if setupLog.Name() != filepath.Join(dir, "experiments", experiment.ID.String(), "setup.log") {
		t.Errorf("setup log = %s", setupLog.Name())
	}
	if _, err := setupLog.WriteString("setup\n"); err != nil {
		t.Fatal(err)
	}
	if err := setupLog.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateExperiment(&experiment); !errors.Is(err, fs.ErrExist) {
		t.Errorf("duplicate experiment error = %v, want fs.ErrExist", err)
	}

	experiment.State = model.StateRunning
	if err := r.UpdateExperiment(&experiment); err != nil {
		t.Fatal(err)
	}
	gotExperiment, err := r.ReadExperiment(experiment.ID)
	if err != nil || gotExperiment.ID != experiment.ID || gotExperiment.State != experiment.State || !reflect.DeepEqual(gotExperiment.Env, experiment.Env) {
		t.Errorf("experiment = %+v, %v; want identity, state, and env from %+v", gotExperiment, err, experiment)
	}

	run := model.NewRun(experiment.ID, experiment.Points[0], 1)
	runLog, err := r.CreateRun(run)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runLog.WriteString("{}\n"); err != nil {
		t.Fatal(err)
	}
	if err := runLog.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateRun(run); !errors.Is(err, fs.ErrExist) {
		t.Errorf("duplicate run error = %v, want fs.ErrExist", err)
	}
	if runs, err := r.Runs(experiment.ID); err != nil || len(runs) != 1 {
		t.Errorf("runs after creation = %+v, %v", runs, err)
	}
	run.State = model.StateDone
	run.End = run.Start
	run.Outcome = model.Outcome{"ok": true}
	if err := r.UpdateRun(run); err != nil {
		t.Fatal(err)
	}
	gotRuns, err := r.Runs(experiment.ID)
	if err != nil || len(gotRuns) != 1 || gotRuns[0].ID != run.ID || gotRuns[0].State != model.StateDone || gotRuns[0].Outcome["ok"] != true {
		t.Errorf("runs = %+v, %v; want completed run %+v", gotRuns, err, run)
	}

	for _, path := range []string{
		filepath.Join(dir, "experiments", experiment.ID.String(), "experiment.json"),
		filepath.Join(dir, "experiments", experiment.ID.String(), "runs", run.ID.String()+".json"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(string(data), "\n") || !strings.Contains(string(data), "\n  \"") {
			t.Errorf("record is not indented and newline-terminated: %q", data)
		}
	}
}

func TestCreateFailureRemovesOnlyCreatedFiles(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	experiment := model.NewExperiment(model.Design{})
	experimentDir := filepath.Join(dir, "experiments", experiment.ID.String())
	if err := os.MkdirAll(experimentDir, 0755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(experimentDir, "setup.log")
	if err := os.WriteFile(logPath, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateExperiment(&experiment); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("CreateExperiment error = %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil || string(data) != "keep" {
		t.Errorf("existing log changed: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(experimentDir, "experiment.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("failed create left a record: %v", err)
	}
}

func TestRunsDiscoversOnlyJSONRecords(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	experiment := model.NewExperiment(model.Design{})
	log, err := r.CreateExperiment(&experiment)
	if err != nil {
		t.Fatal(err)
	}
	_ = log.Close()
	if runs, err := r.Runs(experiment.ID); err != nil || len(runs) != 0 {
		t.Fatalf("empty runs = %+v, %v", runs, err)
	}
	run := model.NewRun(experiment.ID, nil, 1)
	log, err = r.CreateRun(run)
	if err != nil {
		t.Fatal(err)
	}
	_ = log.Close()
	if err := os.WriteFile(filepath.Join(r.runsDir(experiment.ID), ".tmp-record"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if runs, err := r.Runs(experiment.ID); err != nil || len(runs) != 1 || runs[0].ID != run.ID {
		t.Errorf("discovered runs = %+v, %v", runs, err)
	}
	if _, err := r.ReadRun(experiment.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.runPath(experiment.ID, run.ID), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Runs(experiment.ID); err == nil {
		t.Error("malformed run record was ignored")
	}
}

func TestMissingRecordsAndOpenDoNotCreate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "results")
	r := Open(dir)
	if _, err := r.ReadExperiment(uuid.NewV7()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing experiment error = %v", err)
	}
	if _, err := r.ReadRun(uuid.NewV7(), uuid.NewV7()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing run error = %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("reads created results directory: %v", err)
	}
}

func TestUpdateRequiresExistingRecord(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateExperiment(&model.Experiment{ID: uuid.NewV7()}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("UpdateExperiment error = %v", err)
	}
	if err := r.UpdateRun(&model.Run{ID: uuid.NewV7()}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("UpdateRun error = %v", err)
	}
}
