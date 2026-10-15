package results

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/felixge/doe/internal/model"
)

func TestRunIndexReuseLatestAndPreserveExecution(t *testing.T) {
	r := newTestResults(t, t.TempDir())
	index, err := r.OpenRunIndex()
	if err != nil {
		t.Fatal(err)
	}
	experiment := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {1}}, Replicates: 1})
	createStatusExperiment(t, r, &experiment)
	var latest *model.Run
	for range 2 {
		run := model.NewRun(experiment.ID, model.Point{"foo": json.Number("9007199254740993")}, 1)
		run.State, run.End = model.StateDone, run.Start
		run.Outcome = model.Outcome{"precise": json.Number("1.234567890123456789")}
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
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	index, err = r.OpenRunIndex()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = index.Close() }()

	// Reuse twice, with the first copy as the second candidate.
	for range 2 {
		dest := model.NewExperiment(experiment.Design)
		createStatusExperiment(t, r, &dest)
		if copied, err := index.Reuse(dest.ID, "hash"); err != nil || !copied {
			t.Fatalf("Reuse = %v, %v", copied, err)
		}
		want := *latest
		want.ExperimentID, want.OriginalExperimentID = dest.ID, experiment.ID
		expected, err := marshalJSON(&want)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(r.runPath(dest.ID, latest.ID))
		if err != nil || !bytes.Equal(data, expected) {
			t.Fatalf("copied run = %s, %v; want %s", data, err, expected)
		}
		log, err := os.ReadFile(r.runLogPath(dest.ID, latest.ID))
		if err != nil || string(log) != "warning\n{}\n" {
			t.Errorf("copied log = %q, %v", log, err)
		}
		run, err := r.ReadRun(dest.ID, latest.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !run.Reused() {
			t.Error("copied run does not report reuse")
		}
		if err := index.Add("hash", run); err == nil {
			t.Error("reused run was indexed")
		}
		path, err := filepath.Rel(r.dir, r.runPath(dest.ID, latest.ID))
		if err != nil {
			t.Fatal(err)
		}
		index.paths["hash"] = path
	}
	original, err := os.ReadFile(r.runPath(experiment.ID, latest.ID))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := marshalJSON(latest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, expected) {
		t.Error("reuse changed source JSON")
	}
	data, err := os.ReadFile(filepath.Join(r.dir, "runs.index.jsonl"))
	if err != nil || bytes.Count(data, []byte{'\n'}) != 2 {
		t.Errorf("index after copy = %q, %v; want two original executions", data, err)
	}
}

func TestRunIndexPartialLineRecovery(t *testing.T) {
	r := newTestResults(t, t.TempDir())
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
			r := newTestResults(t, t.TempDir())
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
			r := newTestResults(t, t.TempDir())
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
	r := newTestResults(t, t.TempDir())
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
