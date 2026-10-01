package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/felixge/doe/internal/cli"
	"github.com/felixge/doe/internal/model"
	"github.com/felixge/doe/internal/results"
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
	experiment.Start = time.Time{}
	release, err := r.Lock(experiment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	log, err := r.CreateExperiment(&experiment)
	if err != nil {
		t.Fatal(err)
	}
	_ = log.Close()

	code, stdout, stderr := runStatusCommand(t, "status")
	want := "Experiment: " + experiment.ID.String() + "\nState: Setup\n" +
		"Setup log: " + filepath.Join("results", "experiments", experiment.ID.String(), "setup.log") + "\n" +
		"Runs: 0/2 complete (0%)\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Fatalf("setup status = %d, %q, %q; want %q", code, stdout, stderr, want)
	}

	experiment.State = model.StateRunning
	if err := r.UpdateExperiment(&experiment); err != nil {
		t.Fatal(err)
	}
	run := model.NewRun(experiment.ID, experiment.Points[1], 3)
	run.Start = time.Time{}
	log, err = r.CreateRun(run)
	if err != nil {
		t.Fatal(err)
	}
	_ = log.Close()
	if err := r.AppendRun(experiment.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runStatusCommand(t, "status")
	want = "Experiment: " + experiment.ID.String() + "\nState: Running\nRuns: 0/2 complete (0%)\n" +
		"Current run: replicate 3, design point foo=2\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("running status = %d, %q, %q; want %q", code, stdout, stderr, want)
	}
}

func TestStatusCompletedAndFailedExperiments(t *testing.T) {
	for _, tc := range []struct {
		name      string
		setup     string
		wantCode  int
		wantState string
		wantError string
		wantRuns  string
	}{
		{"done", "", 0, "Done", "", "Runs: 1/1 complete (100%)\n"},
		{"setup error", "exit 7", 1, "Error", "setup: exit status 7", "Runs: 0/1 complete (0%)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			args := []string{"experiment", "foo=1", "-r", "echo '{}' ; true '{foo}'"}
			if tc.setup != "" {
				args = append(args, "-s", tc.setup)
			}
			code, id, stderr := runStatusCommand(t, args...)
			if code != tc.wantCode || (code == 0 && stderr != "") || (code != 0 && !strings.Contains(stderr, "exit status 7")) {
				t.Fatalf("experiment = %d, %q, %q", code, id, stderr)
			}
			lockID, err := os.ReadFile(filepath.Join("results", "results.lock"))
			if err != nil {
				t.Fatal(err)
			}
			id = strings.TrimSpace(string(lockID))
			code, stdout, stderr := runStatusCommand(t, "status")
			want := "Experiment: " + id + "\nState: " + tc.wantState + "\n" + tc.wantRuns
			if tc.wantError != "" {
				want += "Error: " + tc.wantError + "\n"
			}
			if code != 0 || stderr != "" || !strings.HasPrefix(stdout, want) {
				t.Errorf("status = %d, %q, %q; want prefix %q", code, stdout, stderr, want)
			}
		})
	}
}

func TestStatusUnfinishedWithoutOwner(t *testing.T) {
	t.Chdir(t.TempDir())
	r, err := results.New("results")
	if err != nil {
		t.Fatal(err)
	}
	experiment := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {1}}, Replicates: 1})
	release, err := r.Lock(experiment.ID)
	if err != nil {
		t.Fatal(err)
	}
	log, err := r.CreateExperiment(&experiment)
	if err != nil {
		t.Fatal(err)
	}
	_ = log.Close()
	if err := release(); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runStatusCommand(t, "status")
	want := "Experiment: " + experiment.ID.String() + "\nState: Stopped\nRuns: 0/1 complete (0%)\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("status = %d, %q, %q; want %q", code, stdout, stderr, want)
	}
}

func TestFormatDuration(t *testing.T) {
	for _, tc := range []struct {
		duration time.Duration
		want     string
	}{
		{0, "0s"}, {200 * time.Millisecond, "0s"}, {-time.Second, "0s"},
		{1500 * time.Millisecond, "2s"}, {3*time.Minute + 42*time.Second, "3m 42s"},
		{time.Hour + 2*time.Minute + 3*time.Second, "1h 2m 3s"}, {2 * time.Hour, "2h"},
	} {
		if got := formatDuration(tc.duration); got != tc.want {
			t.Errorf("formatDuration(%s) = %q, want %q", tc.duration, got, tc.want)
		}
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
}
