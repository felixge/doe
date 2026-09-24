package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/felixge/doe2/internal/cli"
	"github.com/felixge/doe2/internal/model"
	"github.com/felixge/doe2/internal/results"
)

func runStatusCommand(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Main(context.Background(), &cli.Env{Stdout: &stdout, Stderr: &stderr}, args)
	return code, stdout.String(), stderr.String()
}

func TestStatusNoExperiment(t *testing.T) {
	t.Chdir(t.TempDir())
	code, stdout, stderr := runStatusCommand(t, "status")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "no experiment found") {
		t.Errorf("status = %d, %q, %q; want no experiment error", code, stdout, stderr)
	}
	if _, err := os.Stat("results"); !os.IsNotExist(err) {
		t.Errorf("status created results: %v", err)
	}
}

func TestStatusProgress(t *testing.T) {
	t.Chdir(t.TempDir())
	r, err := results.New("results")
	if err != nil {
		t.Fatal(err)
	}
	experiment := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {1, 2}}, Replicates: 1})
	release, err := r.LockExperiment(experiment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	check := func(state, runs string) {
		t.Helper()
		code, stdout, stderr := runStatusCommand(t, "status")
		want := "Experiment: " + experiment.ID.String() + "\nState: " + state + "\n" + runs
		if code != 0 || stdout != want || stderr != "" {
			t.Errorf("status = %d, %q, %q; want %q", code, stdout, stderr, want)
		}
	}
	check("Setup", "")
	if err := r.AppendExperiment(&experiment); err != nil {
		t.Fatal(err)
	}
	check("Running", "Runs: 0/2 complete (0%)\n")
	if err := r.AppendRun(model.NewRun(experiment.ID, experiment.Points[0], 1)); err != nil {
		t.Fatal(err)
	}
	check("Running", "Runs: 1/2 complete (50%)\n")
	failed := model.NewRun(experiment.ID, experiment.Points[1], 1)
	failed.Error = "exit status 7"
	if err := r.AppendRun(failed); err != nil {
		t.Fatal(err)
	}
	check("Error", "Runs: 1/2 complete (50%)\n")
	if err := release(); err != nil {
		t.Fatal(err)
	}
	check("Error", "Runs: 1/2 complete (50%)\n")
}

func TestStatusStopped(t *testing.T) {
	t.Chdir(t.TempDir())
	r, err := results.New("results")
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
	code, stdout, stderr := runStatusCommand(t, "status")
	want := "Experiment: " + experiment.ID.String() + "\nState: Stopped\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("stopped before record = %d, %q, %q; want %q", code, stdout, stderr, want)
	}
	if err := r.AppendExperiment(&experiment); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runStatusCommand(t, "status")
	want += "Runs: 0/1 complete (0%)\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("stopped after record = %d, %q, %q; want %q", code, stdout, stderr, want)
	}
}

func TestStatusCompletedStudy(t *testing.T) {
	t.Chdir(t.TempDir())
	study := filepath.Join(t.TempDir(), "study.yaml")
	if err := os.WriteFile(study, []byte("factors: {foo: [1]}\nrun: echo '{}'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	code, id, stderr := runStatusCommand(t, "experiment", "-f", study)
	if code != 0 || stderr != "" {
		t.Fatalf("experiment = %d, %q, %q", code, id, stderr)
	}
	code, stdout, stderr := runStatusCommand(t, "status", "-f", study)
	want := "Experiment: " + strings.TrimSpace(id) + "\nState: Done\nRuns: 1/1 complete (100%)\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("status = %d, %q, %q; want %q", code, stdout, stderr, want)
	}
	if _, err := os.Stat("results"); !os.IsNotExist(err) {
		t.Errorf("status created results in current directory: %v", err)
	}
}

func TestStatusSetupError(t *testing.T) {
	t.Chdir(t.TempDir())
	code, _, stderr := runStatusCommand(t, "experiment", "foo=1", "-s", "exit 7", "-r", "echo '{}'")
	if code != 1 || !strings.Contains(stderr, "exit status 7") {
		t.Fatalf("setup failure = %d, %q", code, stderr)
	}
	id, err := os.ReadFile(filepath.Join("results", "experiment.lock"))
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runStatusCommand(t, "status")
	want := "Experiment: " + strings.TrimSpace(string(id)) + "\nState: Error\nRuns: 0/1 complete (0%)\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("status = %d, %q, %q; want %q", code, stdout, stderr, want)
	}
}

func TestStatusHelpAndArguments(t *testing.T) {
	for _, args := range [][]string{{"status", "-h"}, {"status", "--help"}} {
		code, stdout, stderr := runStatusCommand(t, args...)
		if code != 0 || !strings.Contains(stdout, "Usage: doe status [-f study.yaml]") || stderr != "" {
			t.Errorf("status help = %d, %q, %q", code, stdout, stderr)
		}
	}
	for _, args := range [][]string{{"status", "unexpected"}, {"status", "--unknown"}} {
		code, stdout, stderr := runStatusCommand(t, args...)
		if code != 1 || stdout != "" || stderr == "" {
			t.Errorf("invalid status args = %d, %q, %q", code, stdout, stderr)
		}
	}
	code, stdout, stderr := runStatusCommand(t)
	if code != 0 || !strings.Contains(stdout, "status      Show the latest experiment's progress") || stderr != "" {
		t.Errorf("root help = %d, %q, %q", code, stdout, stderr)
	}
}
