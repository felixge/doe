package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/felixge/doe/internal/model"
)

func completedCommand(t *testing.T, args ...string) (*model.Experiment, []*model.Run) {
	t.Helper()
	code, stdout, stderr := runCommand(t.Context(), append([]string{"run"}, args...)...)
	if code != 0 || stderr != "" {
		t.Fatalf("experiment = %d, %q, %q", code, stdout, stderr)
	}
	return readCompletedExperiment(t, "results", stdout)
}

func TestExperimentReusesExtendedDesign(t *testing.T) {
	t.Chdir(t.TempDir())
	script := `echo {foo} >> executed; echo '{"ok":true}'`
	setup := "echo setup >> setups"
	first, firstRuns := completedCommand(t, "foo=[1,2]", "-r", script, "-s", setup)
	second, secondRuns := completedCommand(t, "foo=[3,2,1]", "-n", "2", "-r", script, "-s", setup)
	if len(firstRuns) != 2 || len(secondRuns) != 6 || first.ID == second.ID {
		t.Fatalf("experiments = %+v, %+v; runs = %d, %d", first, second, len(firstRuns), len(secondRuns))
	}
	var reused int
	for _, run := range secondRuns {
		if run.ExperimentID != second.ID {
			t.Errorf("run belongs to wrong experiment: %+v", run)
		}
		if run.Replicate == 1 && run.Point["foo"] != float64(3) {
			reused++
			if !run.Reused() || run.OriginalExperimentID != first.ID {
				t.Errorf("reused run lost provenance: %+v", run)
			}
		} else if run.Reused() {
			t.Errorf("new slot was reused: %+v", run)
		}
	}
	if reused != 2 {
		t.Errorf("reused = %d, want 2", reused)
	}
	for path, want := range map[string]int{"executed": 6, "setups": 2, "results/runs.index.jsonl": 6} {
		data, err := os.ReadFile(path)
		if err != nil || bytes.Count(data, []byte{'\n'}) != want {
			t.Errorf("%s = %q, %v; want %d lines", path, data, err, want)
		}
	}
	third, thirdRuns := completedCommand(t, "foo=[3,2,1]", "-n", "2", "-r", script, "-s", setup)
	if len(thirdRuns) != 6 {
		t.Fatalf("third runs = %d", len(thirdRuns))
	}
	for _, run := range thirdRuns {
		if !run.Reused() || run.ExperimentID != third.ID {
			t.Errorf("fully reusable experiment executed a run: %+v", run)
		}
	}
	data, err := os.ReadFile("executed")
	if err != nil || bytes.Count(data, []byte{'\n'}) != 6 {
		t.Errorf("third invocation executed runs: %q, %v", data, err)
	}
}

func TestExperimentDoesNotReuseDifferentExpandedCommands(t *testing.T) {
	t.Chdir(t.TempDir())
	script := `printf '{"argument":"%s"}\n' '{foo}'`
	_, integerRuns := completedCommand(t, "foo=1000000", "-r", script)
	floatExperiment, floatRuns := completedCommand(t, "foo=1000000.0", "-r", script)
	if integerRuns[0].Outcome["argument"] != "1000000" {
		t.Errorf("integer argument = %v", integerRuns[0].Outcome["argument"])
	}
	// JSON encodes these settings identically, but their expanded commands differ.
	if floatRuns[0].Reused() || floatRuns[0].ID == integerRuns[0].ID || floatRuns[0].ExperimentID != floatExperiment.ID ||
		floatRuns[0].Outcome["argument"] != "1e+06" {
		t.Errorf("different expanded command reused integer outcome: %+v", floatRuns[0])
	}
	repeated, repeatedRuns := completedCommand(t, "foo=1000000.0", "-r", script)
	if !repeatedRuns[0].Reused() || repeatedRuns[0].ExperimentID != repeated.ID ||
		repeatedRuns[0].OriginalExperimentID != floatExperiment.ID ||
		repeatedRuns[0].ID != floatRuns[0].ID || repeatedRuns[0].Outcome["argument"] != "1e+06" {
		t.Errorf("identical expanded command was not reused: %+v", repeatedRuns[0])
	}
}

func TestExperimentNoReuseAndLatestMatch(t *testing.T) {
	t.Chdir(t.TempDir())
	args := []string{"foo=1", "-r", "echo run >> executed; echo '{}'; true '{foo}'"}
	first, firstRuns := completedCommand(t, args...)
	second, secondRuns := completedCommand(t, append(args, "--no-reuse")...)
	third, thirdRuns := completedCommand(t, args...)
	fourth, fourthRuns := completedCommand(t, args...)
	if secondRuns[0].Reused() || secondRuns[0].ExperimentID != second.ID || secondRuns[0].ID == firstRuns[0].ID {
		t.Errorf("--no-reuse did not execute a fresh run: %+v", secondRuns[0])
	}
	for i, runs := range [][]*model.Run{thirdRuns, fourthRuns} {
		experiment := []*model.Experiment{third, fourth}[i]
		if !runs[0].Reused() || runs[0].ID != secondRuns[0].ID ||
			runs[0].ExperimentID != experiment.ID || runs[0].OriginalExperimentID != second.ID {
			t.Errorf("latest original execution not reused: %+v", runs[0])
		}
	}
	if first.ID == third.ID || third.ID == fourth.ID {
		t.Error("reuse did not create separate experiments")
	}
	data, err := os.ReadFile("results/runs.index.jsonl")
	if err != nil || bytes.Count(data, []byte{'\n'}) != 2 {
		t.Errorf("index includes copies: %q, %v", data, err)
	}
	data, err = os.ReadFile("executed")
	if err != nil || bytes.Count(data, []byte{'\n'}) != 2 {
		t.Errorf("execution count = %q, %v; want two", data, err)
	}
}

func TestExperimentContinuesFailedExperiment(t *testing.T) {
	t.Chdir(t.TempDir())
	script := `echo {foo} >> executed; echo '{}'; if [ {foo} = 2 ] && [ ! -f gate ]; then exit 7; fi`
	args := []string{"run", "foo=[1,2,3]", "-r", script}
	code, stdout, stderr := runCommand(t.Context(), args...)
	if code != 1 || stderr == "" {
		t.Fatalf("failed experiment = %d, %q, %q", code, stdout, stderr)
	}
	first, firstRuns := readCompletedExperiment(t, "results", stdout)
	if len(firstRuns) != 2 || firstRuns[1].State != model.RunStateError {
		t.Fatalf("failed runs = %+v", firstRuns)
	}
	if err := os.WriteFile("gate", nil, 0600); err != nil {
		t.Fatal(err)
	}
	second, secondRuns := completedCommand(t, args[1:]...)
	if len(secondRuns) != 3 {
		t.Fatalf("continued runs = %d", len(secondRuns))
	}
	for _, run := range secondRuns {
		wantReused := run.Point["foo"] == float64(1)
		if run.Reused() != wantReused || (wantReused && run.OriginalExperimentID != first.ID) ||
			run.State != model.RunStateDone || run.ExperimentID != second.ID || run.ID == firstRuns[1].ID {
			t.Errorf("continued run = %+v", run)
		}
	}
	data, err := os.ReadFile("executed")
	if err != nil || string(data) != "1\n2\n2\n3\n" {
		t.Errorf("continued executions = %q, %v", data, err)
	}
	data, err = os.ReadFile("results/runs.index.jsonl")
	if err != nil || bytes.Count(data, []byte{'\n'}) != 3 {
		t.Errorf("index contains unsuccessful attempts: %q, %v", data, err)
	}
}

func TestExperimentChangedScriptsAndCleanPreventReuse(t *testing.T) {
	for _, tc := range []struct {
		name, setup, run string
		clean            bool
	}{
		{"run", "echo setup", "echo '{}'; true '{foo}'; :", false},
		{"setup", "echo changed", "echo '{}'; true '{foo}'", false},
		{"clean", "echo setup", "echo '{}'; true '{foo}'", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			first, firstRuns := completedCommand(t, "foo=1", "-s", "echo setup", "-r", "echo '{}'; true '{foo}'")
			args := []string{"foo=1", "-s", tc.setup, "-r", tc.run}
			if tc.clean {
				args = append(args, "--clean")
			}
			second, runs := completedCommand(t, args...)
			if len(runs) != 1 || runs[0].Reused() || runs[0].ExperimentID != second.ID || runs[0].ID == firstRuns[0].ID {
				t.Errorf("new execution = %+v", runs)
			}
			if tc.clean {
				if _, err := os.Stat(filepath.Join("results", "experiments", first.ID.String())); !os.IsNotExist(err) {
					t.Errorf("clean retained old experiment: %v", err)
				}
			}
		})
	}
}
