package results

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/felixge/doe/internal/model"
)

func TestRunIndexReuseLatestAndPreserveFiles(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	index, err := r.OpenRunIndex()
	if err != nil {
		t.Fatal(err)
	}
	experiment := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {1}}, Replicates: 1})
	createStatusExperiment(t, r, &experiment)
	var latest *model.Run
	for range 2 {
		run := model.NewRun(experiment.ID, experiment.Points[0], 1)
		run.State, run.End = model.StateDone, run.Start
		log, err := r.CreateRun(run)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := log.WriteString("warning\n{}\n"); err != nil {
			t.Fatal(err)
		}
		_ = log.Close()
		if err := index.Add("hash", run); err != nil {
			t.Fatal(err)
		}
		latest = run
	}
	// Noncanonical whitespace must survive copying, not just JSON decoding.
	recordPath := r.runPath(experiment.ID, latest.ID)
	record, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath, append([]byte(" \n"), record...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	index, err = r.OpenRunIndex()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = index.Close() }()
	dest := model.NewExperiment(experiment.Design)
	createStatusExperiment(t, r, &dest)
	if copied, err := index.Reuse(dest.ID, "hash"); err != nil || !copied {
		t.Fatalf("Reuse = %v, %v", copied, err)
	}
	for _, ext := range []string{".json", ".log"} {
		original, err := os.ReadFile(filepath.Join(r.runsDir(experiment.ID), latest.ID.String()+ext))
		if err != nil {
			t.Fatal(err)
		}
		copied, err := os.ReadFile(filepath.Join(r.runsDir(dest.ID), latest.ID.String()+ext))
		if err != nil || !bytes.Equal(copied, original) {
			t.Errorf("copied %s differs: %q, %v", ext, copied, err)
		}
		originalInfo, err := os.Stat(filepath.Join(r.runsDir(experiment.ID), latest.ID.String()+ext))
		if err != nil {
			t.Fatal(err)
		}
		copiedInfo, err := os.Stat(filepath.Join(r.runsDir(dest.ID), latest.ID.String()+ext))
		if err != nil || os.SameFile(originalInfo, copiedInfo) {
			t.Errorf("copy %s is not independent: %v", ext, err)
		}
	}
	run, err := r.ReadRun(dest.ID, latest.ID)
	if err != nil || run.ExperimentID != experiment.ID || !run.Start.Equal(latest.Start) {
		t.Errorf("copied provenance = %+v, %v", run, err)
	}
	data, err := os.ReadFile(filepath.Join(r.dir, "runs.index.jsonl"))
	if err != nil || bytes.Count(data, []byte{'\n'}) != 2 {
		t.Errorf("index after copy = %q, %v; want two original executions", data, err)
	}
}

func TestRunIndexPartialLineRecovery(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.dir, "runs.index.jsonl")
	complete := []byte("{\"hash\":\"old\",\"path\":\"experiments/old/runs/old.json\"}\n")
	if err := os.WriteFile(path, append(bytes.Clone(complete), []byte("{\"hash\":\"partial")...), 0600); err != nil {
		t.Fatal(err)
	}
	index, err := r.OpenRunIndex()
	if err != nil {
		t.Fatal(err)
	}
	run := model.NewRun(model.NewExperiment(model.Design{}).ID, nil, 1)
	run.State = model.StateDone
	if err := index.Add("new", run); err != nil {
		t.Fatal(err)
	}
	_ = index.Close()
	index, err = r.OpenRunIndex()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = index.Close() }()
	if len(index.paths) != 2 || index.paths["old"] == "" || index.paths["new"] == "" {
		t.Errorf("recovered candidates = %v", index.paths)
	}
}

func TestRunIndexInvalidEntries(t *testing.T) {
	for _, line := range []string{
		"broken", `{"hash":"h","path":"../outside.json"}`,
		`{"hash":"h","path":"/absolute.json"}`, `{"path":"runs/run.json"}`,
		`{"hash":"h","path":"runs/run.log"}`,
	} {
		t.Run(line, func(t *testing.T) {
			r, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(r.dir, "runs.index.jsonl"), []byte(line+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if index, err := r.OpenRunIndex(); err == nil {
				_ = index.Close()
				t.Error("invalid index was accepted")
			}
		})
	}
}

func TestRunIndexOnlyReusesCompleteAvailableRuns(t *testing.T) {
	for _, state := range []model.State{model.StateRunning, model.StateStopped, model.StateError, model.StateDone} {
		t.Run(string(state), func(t *testing.T) {
			r, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			experiment := model.NewExperiment(model.Design{})
			createStatusExperiment(t, r, &experiment)
			run := model.NewRun(experiment.ID, nil, 1)
			run.State = state
			createStatusRun(t, r, run)
			index, err := r.OpenRunIndex()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = index.Close() }()
			if state != model.StateDone {
				if err := index.Add("hash", run); err == nil {
					t.Error("unfinished run was indexed")
				}
			} else {
				// A missing log must turn even a successful record into a miss.
				if err := os.Remove(r.runLogPath(experiment.ID, run.ID)); err != nil {
					t.Fatal(err)
				}
			}
			path, err := filepath.Rel(r.dir, r.runPath(experiment.ID, run.ID))
			if err != nil {
				t.Fatal(err)
			}
			index.paths["hash"] = path
			if copied, err := index.Reuse(model.NewExperiment(model.Design{}).ID, "hash"); err != nil || copied {
				t.Errorf("Reuse = %v, %v; want miss", copied, err)
			}
			index.paths["missing"] = "experiments/missing/runs/missing.json"
			if copied, err := index.Reuse(experiment.ID, "missing"); err != nil || copied {
				t.Errorf("missing candidate = %v, %v", copied, err)
			}
		})
	}
}

func TestRunIndexPathsAreRelative(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	index, err := r.OpenRunIndex()
	if err != nil {
		t.Fatal(err)
	}
	run := model.NewRun(model.NewExperiment(model.Design{}).ID, nil, 1)
	run.State = model.StateDone
	if err := index.Add("hash", run); err != nil {
		t.Fatal(err)
	}
	_ = index.Close()
	data, err := os.ReadFile(filepath.Join(r.dir, "runs.index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var entry indexEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Path != "experiments/"+run.ExperimentID.String()+"/runs/"+run.ID.String()+".json" {
		t.Errorf("indexed path = %q", entry.Path)
	}
}
