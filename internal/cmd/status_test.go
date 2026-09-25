package cmd

import (
	"bytes"
	"context"
	"fmt"
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
	// Legacy experiment records do not have a start timestamp.
	experiment.Start = time.Time{}
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
	check("Running", "Runs: 0/2 complete (0%)\nCurrent run: replicate 1, design point foo=1\n")
	if err := r.AppendRun(model.NewRun(experiment.ID, experiment.Points[0], 1)); err != nil {
		t.Fatal(err)
	}
	check("Running", "Runs: 1/2 complete (50%)\nCurrent run: replicate 1, design point foo=2\n")
	failed := model.NewRun(experiment.ID, experiment.Points[1], 1)
	failed.Error = "exit status 7"
	if err := r.AppendRun(failed); err != nil {
		t.Fatal(err)
	}
	check("Error", "Runs: 1/2 complete (50%)\nError: exit status 7\n")
	if err := release(); err != nil {
		t.Fatal(err)
	}
	check("Error", "Runs: 1/2 complete (50%)\nError: exit status 7\n")
}

func TestStatusCurrentRunFollowsSchedule(t *testing.T) {
	t.Chdir(t.TempDir())
	r, err := results.New("results")
	if err != nil {
		t.Fatal(err)
	}
	experiment := model.NewExperiment(model.Design{
		Factors: model.Factors{"foo": {1, 2, 3}, "bar": {"x"}}, Replicates: 2,
	})
	experiment.Start = time.Time{}
	release, err := r.LockExperiment(experiment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	if err := r.AppendExperiment(&experiment); err != nil {
		t.Fatal(err)
	}
	for index, point := range []int{0, 1, 2, 1, 2, 0} {
		code, stdout, stderr := runStatusCommand(t, "status")
		want := fmt.Sprintf("Current run: replicate %d, design point bar=x foo=%d\n", index/3+1, point+1)
		if code != 0 || stderr != "" || !strings.HasSuffix(stdout, want) {
			t.Errorf("status at run %d = %d, %q, %q; want suffix %q", index, code, stdout, stderr, want)
		}
		if err := r.AppendRun(model.NewRun(experiment.ID, experiment.Points[point], index/3+1)); err != nil {
			t.Fatal(err)
		}
	}
	code, stdout, stderr := runStatusCommand(t, "status")
	if code != 0 || stderr != "" || strings.Contains(stdout, "Current run:") {
		t.Errorf("completed status = %d, %q, %q; want no current run", code, stdout, stderr)
	}
}

func TestStatusShowsJustStartedTime(t *testing.T) {
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
	defer func() { _ = release() }()
	if err := r.AppendExperiment(&experiment); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runStatusCommand(t, "status")
	want := "Experiment: " + experiment.ID.String() + "\nState: Running\nRuns: 0/1 complete (0%)\nCurrent run: replicate 1, design point foo=1\nExperiment elapsed: "
	if code != 0 || !strings.HasPrefix(stdout, want) || stderr != "" {
		t.Errorf("status = %d, %q, %q; want %q followed by duration", code, stdout, stderr, want)
	}
}

func TestStatusShowsEstimatedRemaining(t *testing.T) {
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
	experiment.Start = time.Now().Add(-20 * time.Second)
	if err := r.AppendExperiment(&experiment); err != nil {
		t.Fatal(err)
	}
	run := model.NewRun(experiment.ID, experiment.Points[0], 1)
	run.Start = experiment.Start.Add(8 * time.Second)
	run.End = run.Start.Add(10 * time.Second)
	if err := r.AppendRun(run); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runStatusCommand(t, "status")
	prefix := "Experiment: " + experiment.ID.String() + "\nState: Running\nRuns: 1/2 complete (50%)\nCurrent run: replicate 1, design point foo=2\n"
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, prefix) {
		t.Fatalf("status = %d, %q, %q; want %q followed by elapsed and remaining times", code, stdout, stderr, prefix)
	}
	lines := strings.Split(strings.TrimSpace(strings.TrimPrefix(stdout, prefix)), "\n")
	if len(lines) != 3 {
		t.Fatalf("status times = %q; want run elapsed, experiment elapsed, and experiment remaining", stdout)
	}
	for i, tc := range []struct {
		label string
		min   time.Duration
		max   time.Duration
	}{
		{"Run elapsed: ", time.Second, 4 * time.Second},
		{"Experiment elapsed: ", 18 * time.Second, 22 * time.Second},
		{"Experiment remaining: ", 6 * time.Second, 9 * time.Second},
	} {
		if !strings.HasPrefix(lines[i], tc.label) {
			t.Errorf("time line %q; want %q", lines[i], tc.label)
			continue
		}
		duration, err := time.ParseDuration(strings.TrimPrefix(lines[i], tc.label))
		if err != nil || duration < tc.min || duration > tc.max {
			t.Errorf("time line %q = %s, %v; want %s to %s", lines[i], duration, err, tc.min, tc.max)
		}
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runStatusCommand(t, "status")
	want := "Experiment: " + experiment.ID.String() + "\nState: Stopped\nRuns: 1/2 complete (50%)\n"
	if code != 0 || stderr != "" || stdout != want {
		t.Errorf("stopped status = %d, %q, %q; want %q", code, stdout, stderr, want)
	}
}

func TestFormatDuration(t *testing.T) {
	for _, tc := range []struct {
		duration time.Duration
		want     string
	}{
		{0, "0s"},
		{200 * time.Millisecond, "0s"},
		{-time.Second, "0s"},
		{1500 * time.Millisecond, "2s"},
		{3*time.Minute + 42*time.Second, "3m 42s"},
		{time.Hour + 2*time.Minute + 3*time.Second, "1h 2m 3s"},
		{2 * time.Hour, "2h"},
	} {
		if got := formatDuration(tc.duration); got != tc.want {
			t.Errorf("formatDuration(%s) = %q, want %q", tc.duration, got, tc.want)
		}
	}
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
	if err := os.WriteFile(study, []byte("factors: {foo: [1]}\nrun: sleep 1; echo '{}' ; true '{foo}'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	code, id, stderr := runStatusCommand(t, "experiment", "-f", study)
	if code != 0 || stderr != "" {
		t.Fatalf("experiment = %d, %q, %q", code, id, stderr)
	}
	code, stdout, stderr := runStatusCommand(t, "status", "-f", study)
	want := "Experiment: " + strings.TrimSpace(id) + "\nState: Done\nRuns: 1/1 complete (100%)\nExperiment elapsed: "
	if code != 0 || !strings.HasPrefix(stdout, want) || stderr != "" || strings.Contains(stdout, "Run elapsed:") || strings.Contains(stdout, "Experiment remaining:") {
		t.Errorf("status = %d, %q, %q; want %q followed by duration", code, stdout, stderr, want)
	}
	if _, err := os.Stat("results"); !os.IsNotExist(err) {
		t.Errorf("status created results in current directory: %v", err)
	}
}

func TestStatusSetupError(t *testing.T) {
	t.Chdir(t.TempDir())
	code, _, stderr := runStatusCommand(t, "experiment", "foo=1", "-s", "exit 7", "-r", "echo '{}' ; true '{foo}'")
	if code != 1 || !strings.Contains(stderr, "exit status 7") {
		t.Fatalf("setup failure = %d, %q", code, stderr)
	}
	id, err := os.ReadFile(filepath.Join("results", "experiment.lock"))
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runStatusCommand(t, "status")
	want := "Experiment: " + strings.TrimSpace(string(id)) + "\nState: Error\nRuns: 0/1 complete (0%)\nError: setup: exit status 7\n"
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
