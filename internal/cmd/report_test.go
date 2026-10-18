package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/felixge/doe/internal/model"
	"github.com/felixge/doe/internal/results"
	"gopkg.in/yaml.v3"
)

func TestReportConfiguration(t *testing.T) {
	t.Chdir(t.TempDir())
	study := `factors: {foo: 1}
run: echo '{}'; true '{foo}'
report: echo study
presets:
  inherited: {}
  full:
    report: echo preset
`
	if err := os.WriteFile("study.yaml", []byte(study), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want model.Script
	}{
		{"study", nil, "echo study"},
		{"inherited", []string{"-p", "inherited"}, "echo study"},
		{"preset", []string{"-p", "full"}, "echo preset"},
		{"CLI", []string{"-p", "full", "--report", "  echo CLI  "}, "echo CLI"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"run", "-f", "study.yaml", "--design"}, tc.args...)
			code, stdout, stderr := runCommand(t.Context(), args...)
			if code != 0 || stderr != "" {
				t.Fatalf("design = %d, %q, %q", code, stdout, stderr)
			}
			var design model.Design
			if err := yaml.Unmarshal([]byte(stdout), &design); err != nil {
				t.Fatal(err)
			}
			if design.Report != tc.want {
				t.Errorf("report = %q, want %q", design.Report, tc.want)
			}
		})
	}
	if _, err := os.Stat("results"); !os.IsNotExist(err) {
		t.Errorf("--design created results: %v", err)
	}
}

func TestExperimentReport(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DOE_EXPERIMENT_DIR", "must-be-overridden")
	if err := os.Mkdir("study dir", 0755); err != nil {
		t.Fatal(err)
	}
	study := `factors: {foo: [1, 2]}
run: echo '{}'; true '{foo}'
report: |
  set -eu
  grep -q '"state": "reporting"' "$DOE_EXPERIMENT_DIR/experiment.json"
  set -- "$DOE_EXPERIMENT_DIR"/runs/*.json
  test "$#" -eq 2
  for run do
    grep -q '"state": "done"' "$run"
  done
  printf '%s\n' "$DOE_EXPERIMENT_DIR" "$PWD" > "$DOE_EXPERIMENT_DIR/report.txt"
  cd /
  test -f "$DOE_EXPERIMENT_DIR/experiment.json"
  echo 'summary {foo}'
  echo warning >&2
`
	if err := os.WriteFile(filepath.Join("study dir", "study.yaml"), []byte(study), 0600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCommand(t.Context(), "run", "-f", filepath.Join("study dir", "study.yaml"))
	if code != 0 || stderr != "" {
		t.Fatalf("experiment = %d, %q, %q", code, stdout, stderr)
	}
	r := results.OpenReadOnly(filepath.Join("study dir", "results"))
	experiment, runs := readCompletedExperiment(t, filepath.Join("study dir", "results"), stdout)
	if experiment.State != model.ExperimentStateDone || experiment.Report == "" || len(runs) != 2 {
		t.Fatalf("experiment = %+v, runs = %+v", experiment, runs)
	}
	log, err := os.ReadFile(r.ReportLogPath(experiment.ID))
	if err != nil || string(log) != "summary {foo}\nwarning\n" {
		t.Errorf("report log = %q, %v", log, err)
	}
	dir, err := filepath.Abs(r.ExperimentDir(experiment.ID))
	if err != nil {
		t.Fatal(err)
	}
	projectDir, err := filepath.Abs("study dir")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "report.txt"))
	if err != nil || string(data) != dir+"\n"+projectDir+"\n" {
		t.Errorf("report paths = %q, %v", data, err)
	}
}

func TestExperimentSkipsReport(t *testing.T) {
	for _, tc := range []struct {
		name, setup, run, report string
		canceled                 bool
		code                     int
		state                    model.ExperimentState
	}{
		{"optional", "", "echo '{}'; true '{foo}'", "", false, 0, model.ExperimentStateDone},
		{"blank", "", "echo '{}'; true '{foo}'", "  ", false, 0, model.ExperimentStateDone},
		{"setup failure", "exit 7", "echo '{}'; true '{foo}'", "touch report-ran", false, 1, model.ExperimentStateError},
		{"run failure", "", "true '{foo}'; exit 7", "touch report-ran", false, 1, model.ExperimentStateError},
		{"invalid outcome", "", "echo invalid; true '{foo}'", "touch report-ran", false, 1, model.ExperimentStateError},
		{"canceled", "", "echo '{}'; true '{foo}'", "touch report-ran", true, 130, model.ExperimentStateStopped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			code, stdout, stderr := runCommand(ctx, "run", "foo=1", "-s", tc.setup, "-r", tc.run, "--report", tc.report)
			if code != tc.code {
				t.Fatalf("experiment = %d, %q, %q", code, stdout, stderr)
			}
			experiment, _ := readCompletedExperiment(t, "results", stdout)
			if experiment.State != tc.state {
				t.Errorf("state = %q, want %q", experiment.State, tc.state)
			}
			if _, err := os.Stat("report-ran"); !os.IsNotExist(err) {
				t.Errorf("report executed: %v", err)
			}
			path := results.OpenReadOnly("results").ReportLogPath(experiment.ID)
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("skipped report created log: %v", err)
			}
		})
	}
}

func TestExperimentReportFailureAndReuse(t *testing.T) {
	t.Chdir(t.TempDir())
	script := "echo '{}'; true '{foo}'"
	code, stdout, stderr := runCommand(t.Context(), "run", "foo=1", "-r", script, "--report", "echo failed; exit 7")
	if code != 1 || !strings.Contains(stderr, "report: exit status 7") {
		t.Fatalf("experiment = %d, %q, %q", code, stdout, stderr)
	}
	first, runs := readCompletedExperiment(t, "results", stdout)
	if first.State != model.ExperimentStateError || first.Error != "report: exit status 7" || len(runs) != 1 || runs[0].State != model.RunStateDone {
		t.Fatalf("failed report = %+v, runs = %+v", first, runs)
	}
	r := results.OpenReadOnly("results")
	if log, err := os.ReadFile(r.ReportLogPath(first.ID)); err != nil || string(log) != "failed\n" {
		t.Errorf("failed report log = %q, %v", log, err)
	}
	second, reused := completedCommand(t, "foo=1", "-r", script, "--report", "echo second")
	if len(reused) != 1 || !reused[0].Reused() || reused[0].ID != runs[0].ID {
		t.Fatalf("changed report did not reuse successful run: %+v", reused)
	}
	if log, err := os.ReadFile(r.ReportLogPath(second.ID)); err != nil || string(log) != "second\n" {
		t.Errorf("reused experiment report log = %q, %v", log, err)
	}
}

func TestExperimentReportCancellationAndStatus(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type commandResult struct {
		code           int
		stdout, stderr string
	}
	done := make(chan commandResult, 1)
	go func() {
		code, stdout, stderr := runCommand(ctx, "run", "foo=1", "-r", "echo '{}'; true '{foo}'",
			"--report", "echo started; touch report-started; exec sleep 30")
		done <- commandResult{code, stdout, stderr}
	}()
	deadline := time.After(5 * time.Second)
	for {
		if _, err := os.Stat("report-started"); err == nil {
			break
		}
		select {
		case result := <-done:
			t.Fatalf("report exited before starting: %+v", result)
		case <-deadline:
			t.Fatal("report did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	r := results.OpenReadOnly("results")
	status, err := r.Status()
	if err != nil || status.State != results.StateReporting || status.Completed != 1 || status.Total != 1 || status.Remaining != 0 {
		t.Fatalf("reporting status = %+v, %v", status, err)
	}
	code, stdout, stderr := runStatusCommand(t, "status")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "State: Reporting\nReport log: "+r.ReportLogPath(status.ExperimentID)+"\n") {
		t.Errorf("reporting status = %d, %q, %q", code, stdout, stderr)
	}
	code, _, stderr = runCommand(t.Context(), "run", "foo=1", "-r", "echo '{}'; true '{foo}'")
	if code != 1 || !strings.Contains(stderr, "another experiment is running") {
		t.Errorf("report did not hold results lock: %d, %q", code, stderr)
	}
	cancel()
	select {
	case result := <-done:
		if result.code != 130 || result.stderr != "" {
			t.Errorf("canceled report = %+v", result)
		}
		experiment, runs := readCompletedExperiment(t, "results", result.stdout)
		if experiment.State != model.ExperimentStateStopped || experiment.Error != "" || experiment.End.IsZero() || len(runs) != 1 || runs[0].State != model.RunStateDone {
			t.Errorf("canceled report = %+v, runs = %+v", experiment, runs)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("report did not stop")
	}
}
