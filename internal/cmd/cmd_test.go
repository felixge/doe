package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/felixge/doe/internal/cli"
	"github.com/felixge/doe/internal/model"
	"github.com/felixge/doe/internal/results"
	"gopkg.in/yaml.v3"
	"uuid"
)

func runCommand(ctx context.Context, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Main(ctx, &cli.Env{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}, args)
	return code, stdout.String(), stderr.String()
}

func readCompletedExperiment(t *testing.T, dir, stdout string) (*model.Experiment, []*model.Run) {
	t.Helper()
	id, err := uuid.Parse(strings.TrimSpace(stdout))
	if err != nil {
		t.Fatal(err)
	}
	r := results.OpenReadOnly(dir)
	experiment, err := r.ReadExperiment(id)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := r.Runs(id)
	if err != nil {
		t.Fatal(err)
	}
	return experiment, runs
}

func TestExperimentIntegration(t *testing.T) {
	dir := t.TempDir()
	studyPath := filepath.Join(dir, "study.yaml")
	study := `factors:
  foo: [1, 2, 3]
  bar: [4, 5]
run: |
  printf '{"result":%s}\n' "$(({foo} + {bar}))"
`
	if err := os.WriteFile(studyPath, []byte(study), 0600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCommand(context.Background(), "run", "-f", studyPath)
	if code != 0 || stderr != "" {
		t.Fatalf("experiment = %d, %q, %q", code, stdout, stderr)
	}
	experiment, runs := readCompletedExperiment(t, filepath.Join(dir, "results"), stdout)
	if experiment.State != model.ExperimentStateDone || len(experiment.Points) != 6 || experiment.SetupEnd.Before(experiment.Start) || experiment.End.Before(experiment.SetupEnd) {
		t.Errorf("experiment = %+v", experiment)
	}
	if len(runs) != 6 {
		t.Fatalf("runs = %d, want 6", len(runs))
	}
	for _, run := range runs {
		foo := int(run.Point["foo"].(float64))
		bar := int(run.Point["bar"].(float64))
		result := int(run.Outcome["result"].(float64))
		if run.ExperimentID != experiment.ID || run.State != model.RunStateDone || result != foo+bar || run.End.Before(run.Start) {
			t.Errorf("run = %+v", run)
		}
		if _, err := os.Stat(filepath.Join(dir, "results", "experiments", experiment.ID.String(), "runs", run.ID.String()+".log")); err != nil {
			t.Errorf("missing run log: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "results", "experiments", experiment.ID.String(), "runs.ids")); !os.IsNotExist(err) {
		t.Errorf("unexpected membership file: %v", err)
	}
}

func TestExperimentSetup(t *testing.T) {
	dir := t.TempDir()
	studyPath := filepath.Join(dir, "study.yaml")
	study := "factors: {foo: [1]}\nsetup: echo marker > marker; echo warning; echo '{\"os\":\"test\"}'\nrun: test -f marker; echo '{}'; true '{foo}'\n"
	if err := os.WriteFile(studyPath, []byte(study), 0600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCommand(context.Background(), "run", "-f", studyPath)
	if code != 0 || stderr != "" {
		t.Fatalf("experiment = %d, %q, %q", code, stdout, stderr)
	}
	experiment, _ := readCompletedExperiment(t, filepath.Join(dir, "results"), stdout)
	if !reflect.DeepEqual(experiment.Env, model.Env{"os": "test"}) || experiment.State != model.ExperimentStateDone {
		t.Errorf("experiment = %+v", experiment)
	}
	log, err := os.ReadFile(filepath.Join(dir, "results", "experiments", experiment.ID.String(), "setup.log"))
	if err != nil || string(log) != "warning\n{\"os\":\"test\"}\n" {
		t.Errorf("setup log = %q, %v", log, err)
	}
}

func TestExperimentWithoutSetupCreatesEmptyLog(t *testing.T) {
	t.Chdir(t.TempDir())
	code, stdout, stderr := runCommand(context.Background(), "run", "foo=1", "-r", "echo '{}'; true '{foo}'")
	if code != 0 || stderr != "" {
		t.Fatalf("experiment = %d, %q, %q", code, stdout, stderr)
	}
	experiment, _ := readCompletedExperiment(t, "results", stdout)
	log, err := os.ReadFile(filepath.Join("results", "experiments", experiment.ID.String(), "setup.log"))
	if err != nil || len(log) != 0 {
		t.Errorf("setup log = %q, %v; want empty", log, err)
	}
}

func TestExperimentSetupFailureAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ctx       func() context.Context
		script    string
		wantCode  int
		wantState model.ExperimentState
		wantError string
	}{
		{"failure", func() context.Context { return context.Background() }, "echo failed; exit 7", 1, model.ExperimentStateError, "setup: exit status 7"},
		{"canceled", func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }, "echo '{}'", 130, model.ExperimentStateStopped, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			code, stdout, stderr := runCommand(tc.ctx(), "run", "foo=1", "-s", tc.script, "-r", "echo '{}'; true '{foo}'")
			if code != tc.wantCode || (tc.wantError != "" && !strings.Contains(stderr, "exit status 7")) {
				t.Errorf("experiment = %d, %q, %q", code, stdout, stderr)
			}
			experiment, runs := readCompletedExperiment(t, "results", stdout)
			if experiment.State != tc.wantState || experiment.Error != tc.wantError || experiment.End.IsZero() || len(runs) != 0 {
				t.Errorf("experiment = %+v, runs = %+v", experiment, runs)
			}
		})
	}
}

func TestExperimentSetupCanceledWhileRunning(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct {
		code           int
		stdout, stderr string
	}, 1)
	go func() {
		code, stdout, stderr := runCommand(ctx, "run", "foo=1", "-s", "touch setup-started; exec sleep 30", "-r", "echo '{}'; true '{foo}'")
		done <- struct {
			code           int
			stdout, stderr string
		}{code, stdout, stderr}
	}()
	deadline := time.After(5 * time.Second)
	for {
		if _, err := os.Stat("setup-started"); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("setup did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	result := <-done
	if result.code != 130 || result.stderr != "" {
		t.Errorf("canceled setup = %+v", result)
	}
	experiment, runs := readCompletedExperiment(t, "results", result.stdout)
	if experiment.State != model.ExperimentStateStopped || experiment.Error != "" || experiment.End.IsZero() || len(runs) != 0 {
		t.Errorf("experiment = %+v, runs = %+v", experiment, runs)
	}
}

func TestExperimentRunCanceledRecordsStoppedRun(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct {
		code           int
		stdout, stderr string
	}, 1)
	args := []string{"run", "foo=[1,2]", "-r",
		`if [ {foo} = 1 ]; then echo '{}'; else printf partial; touch started; while [ ! -f resume ]; do sleep 0.01; done; printf '\n{}\n'; fi`,
		"--report", "touch report-ran"}
	go func() {
		code, stdout, stderr := runCommand(ctx, args...)
		done <- struct {
			code           int
			stdout, stderr string
		}{code, stdout, stderr}
	}()
	deadline := time.After(5 * time.Second)
	for {
		if _, err := os.Stat("started"); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("second run did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	result := <-done
	if result.code != 130 || result.stderr != "" {
		t.Errorf("canceled experiment = %+v", result)
	}
	experiment, runs := readCompletedExperiment(t, "results", result.stdout)
	if experiment.State != model.ExperimentStateStopped || len(runs) != 2 || runs[0].State != model.RunStateDone || runs[1].State != model.RunStateStopped || !runs[1].End.After(runs[1].Start) {
		t.Errorf("experiment = %+v, runs = %+v", experiment, runs)
	}
	if _, err := os.Stat("report-ran"); !os.IsNotExist(err) {
		t.Errorf("report ran after cancellation: %v", err)
	}
	if _, err := os.Stat(filepath.Join("results", "experiments", experiment.ID.String(), "report.log")); !os.IsNotExist(err) {
		t.Errorf("canceled experiment created report log: %v", err)
	}
	log, err := os.ReadFile(filepath.Join("results", "experiments", experiment.ID.String(), "runs", runs[1].ID.String()+".log"))
	if err != nil || string(log) != "partial" {
		t.Errorf("interrupted log = %q, %v", log, err)
	}
	if err := os.WriteFile("resume", nil, 0600); err != nil {
		t.Fatal(err)
	}
	continued, continuedRuns := completedCommand(t, args[1:]...)
	if len(continuedRuns) != 2 || !continuedRuns[0].Reused() || continuedRuns[0].ID != runs[0].ID ||
		continuedRuns[0].ExperimentID != continued.ID || continuedRuns[0].OriginalExperimentID != experiment.ID ||
		continuedRuns[1].Reused() || continuedRuns[1].ID == runs[1].ID || continuedRuns[1].ExperimentID != continued.ID {
		t.Errorf("continued canceled experiment = %+v", continuedRuns)
	}
}

func TestExperimentPresetsAndSchedule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "study.yaml")
	study := `factors: {foo: [0, 1, 2]}
run: echo '{}'; true '{foo}'
replicates: 1
presets:
  full:
    replicates: 2
`
	if err := os.WriteFile(path, []byte(study), 0600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCommand(context.Background(), "run", "-f", path, "-p", "full")
	if code != 0 || stderr != "" {
		t.Fatalf("experiment = %d, %q, %q", code, stdout, stderr)
	}
	experiment, runs := readCompletedExperiment(t, filepath.Join(dir, "results"), stdout)
	if experiment.Preset != "full" || experiment.Replicates != 2 || len(runs) != 6 {
		t.Fatalf("experiment = %+v, runs = %d", experiment, len(runs))
	}
	want := [][2]int{{0, 1}, {1, 1}, {2, 1}, {1, 2}, {2, 2}, {0, 2}}
	for index, run := range runs {
		got := [2]int{int(run.Point["foo"].(float64)), run.Replicate}
		if got != want[index] {
			t.Errorf("run %d = %v, want %v", index, got, want[index])
		}
	}
}

func TestExperimentDesign(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "study.yaml")
	study := `factors: {foo: [1, 2], bar: true}
setup: "  touch setup-ran  "
run: "  touch run-ran; true '{foo}' '{bar}'  "
report: "  touch report-ran  "
replicates: 3
presets:
  full:
    factors: {foo: [3, 4]}
    replicates: 5
`
	if err := os.WriteFile(path, []byte(study), 0600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCommand(context.Background(), "run", "-f", path, "-p", "full", "foo=[7,8]", "-n", "2", "--design")
	if code != 0 || stderr != "" {
		t.Fatalf("design = %d, %q, %q", code, stdout, stderr)
	}
	var got model.Design
	if err := yaml.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	want := model.Design{Factors: model.Factors{"foo": {7, 8}, "bar": {true}}, Setup: "touch setup-ran", Run: "touch run-ran; true '{foo}' '{bar}'", Report: "touch report-ran", Replicates: 2}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("design = %+v, want %+v", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "results")); !os.IsNotExist(err) {
		t.Errorf("--design created results: %v", err)
	}
}

func TestRunOutputErrors(t *testing.T) {
	for _, tc := range []struct {
		name, script string
	}{
		{"empty", `:`}, {"non-JSON", `echo nope`}, {"blank final line", `printf '{}\n\n'`},
		{"malformed", `echo '{broken}'`}, {"null", `echo null`}, {"array", `echo '[1]'`}, {"number", `echo 42`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			code, stdout, _ := runCommand(context.Background(), "run", "foo=1", "-r", "true '{foo}'; "+tc.script)
			if code != 1 {
				t.Fatalf("experiment = %d, %q", code, stdout)
			}
			experiment, runs := readCompletedExperiment(t, "results", stdout)
			if experiment.State != model.ExperimentStateError || len(runs) != 1 || runs[0].State != model.RunStateError || runs[0].Error == "" {
				t.Errorf("experiment = %+v, runs = %+v", experiment, runs)
			}
		})
	}
}

func TestNestedOutcomeAllowsFieldCollisions(t *testing.T) {
	t.Chdir(t.TempDir())
	code, stdout, stderr := runCommand(context.Background(), "run", "error=input", "-r", `echo '{"error":"measurement"}'; true '{error}'`)
	if code != 0 || stderr != "" {
		t.Fatalf("experiment = %d, %q, %q", code, stdout, stderr)
	}
	_, runs := readCompletedExperiment(t, "results", stdout)
	if runs[0].Point["error"] != "input" || runs[0].Outcome["error"] != "measurement" {
		t.Errorf("run = %+v", runs[0])
	}
}

func TestFailedRunKeepsOutcomeAndStopsSchedule(t *testing.T) {
	t.Chdir(t.TempDir())
	code, stdout, stderr := runCommand(context.Background(), "run", "foo=[1,2]", "-r", `echo '{"value":42}'; true '{foo}'; exit 7`)
	if code != 1 || !strings.Contains(stderr, "exit status 7") {
		t.Fatalf("experiment = %d, %q, %q", code, stdout, stderr)
	}
	experiment, runs := readCompletedExperiment(t, "results", stdout)
	if experiment.State != model.ExperimentStateError || len(runs) != 1 || runs[0].State != model.RunStateError || runs[0].Outcome["value"] != float64(42) || !strings.Contains(runs[0].Error, "exit status 7") {
		t.Errorf("experiment = %+v, runs = %+v", experiment, runs)
	}
}

func TestRunOutcomeWithoutFinalNewline(t *testing.T) {
	t.Chdir(t.TempDir())
	code, stdout, stderr := runCommand(context.Background(), "run", "foo=1", "-r", `echo warning; true '{foo}'; printf '{"ok":true}'`)
	if code != 0 || stderr != "" {
		t.Fatalf("experiment = %d, %q, %q", code, stdout, stderr)
	}
	_, runs := readCompletedExperiment(t, "results", stdout)
	if runs[0].Outcome["ok"] != true {
		t.Errorf("run = %+v", runs[0])
	}
}

func TestExperimentVisibleWhileRunning(t *testing.T) {
	t.Chdir(t.TempDir())
	done := make(chan struct {
		code           int
		stdout, stderr string
	}, 1)
	go func() {
		code, stdout, stderr := runCommand(context.Background(), "run", "foo=1", "-r", `touch started; while [ ! -f gate ]; do sleep 0.01; done; echo '{}'; true '{foo}'`)
		done <- struct {
			code           int
			stdout, stderr string
		}{code, stdout, stderr}
	}()
	deadline := time.After(5 * time.Second)
	for {
		if _, err := os.Stat("started"); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("run did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	status, err := results.OpenReadOnly("results").Status()
	if err != nil || status == nil || status.State != results.StateRunning || status.RunPoint.String() != "foo=1" {
		t.Errorf("status = %+v, %v", status, err)
	}
	if err := os.WriteFile("gate", nil, 0600); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.code != 0 || result.stderr != "" {
		t.Errorf("experiment = %+v", result)
	}
}

func TestRunDoesNotReadCLIStdin(t *testing.T) {
	t.Chdir(t.TempDir())
	var stdout, stderr bytes.Buffer
	code := Main(context.Background(), &cli.Env{Stdin: strings.NewReader("injected\n"), Stdout: &stdout, Stderr: &stderr},
		[]string{"run", "foo=1", "-r", `if read value; then echo read; else echo no-input; fi; echo '{}'; true '{foo}'`})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("experiment = %d, %q", code, &stderr)
	}
	experiment, runs := readCompletedExperiment(t, "results", stdout.String())
	log, err := os.ReadFile(filepath.Join("results", "experiments", experiment.ID.String(), "runs", runs[0].ID.String()+".log"))
	if err != nil || string(log) != "no-input\n{}\n" {
		t.Errorf("log = %q, %v", log, err)
	}
}

func TestExperimentCleanPreservesLock(t *testing.T) {
	t.Chdir(t.TempDir())
	args := []string{"run", "foo=1", "-r", "echo '{}'; true '{foo}'"}
	code, _, stderr := runCommand(context.Background(), args...)
	if code != 0 || stderr != "" {
		t.Fatal(stderr)
	}
	lockPath := filepath.Join("results", "results.lock")
	before, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("results", "old"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCommand(context.Background(), append(args, "--clean")...)
	if code != 0 || stderr != "" {
		t.Fatalf("clean = %d, %q, %q", code, stdout, stderr)
	}
	after, err := os.Stat(lockPath)
	if err != nil || !os.SameFile(before, after) {
		t.Errorf("clean replaced lock: %v", err)
	}
	if _, err := os.Stat(filepath.Join("results", "old")); !os.IsNotExist(err) {
		t.Errorf("clean kept old file: %v", err)
	}
	experiment, runs := readCompletedExperiment(t, "results", stdout)
	if experiment.State != model.ExperimentStateDone || len(runs) != 1 {
		t.Errorf("clean experiment = %+v, runs = %+v", experiment, runs)
	}
}

func TestPrepareExperimentAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "study.yaml")
	study := `factors: {foo: [1], bar: [2]}
setup: echo study
run: echo study; true '{foo}' '{bar}'
replicates: 4
presets:
  full:
    factors: {foo: [3], baz: [4]}
    setup: echo preset
`
	if err := os.WriteFile(path, []byte(study), 0600); err != nil {
		t.Fatal(err)
	}
	experiment, err := prepareExperiment(path, "full", "", "echo CLI; true '{foo}' '{bar}' '{baz}'", "", 2, []string{"bar=[5,6]"})
	if err != nil {
		t.Fatal(err)
	}
	if experiment.Preset != "full" || experiment.Setup != "echo preset" || experiment.Replicates != 2 ||
		!reflect.DeepEqual(experiment.Factors, model.Factors{"foo": {3}, "bar": {5, 6}, "baz": {4}}) {
		t.Errorf("experiment = %+v", experiment)
	}
	if _, err := prepareExperiment(path, "missing", "", "", "", 0, nil); err == nil || !strings.Contains(err.Error(), "unknown preset") {
		t.Errorf("unknown preset error = %v", err)
	}
}

func TestLastLogLine(t *testing.T) {
	longWarning := strings.Repeat("x", 2*4096)
	for _, tc := range []struct {
		name, content, want string
	}{
		{"empty", "", ""},
		{"final newline", "warning\n{}\n", "{}"},
		{"no final newline", "warning\n{}", "{}"},
		{"blank last line", "{}\n\n", ""},
		{"large preceding output", longWarning + "\n{\"ok\":true}\n", "{\"ok\":true}"},
		{"long final line", "warning\n" + longWarning + "\n", longWarning},
		{"long single line", longWarning, longWarning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "run.log")
			log, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = log.Close() }()
			if _, err := log.WriteString(tc.content); err != nil {
				t.Fatal(err)
			}
			got, err := lastLogLine(log)
			if err != nil || string(got) != tc.want {
				t.Errorf("lastLogLine() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestStopReason(t *testing.T) {
	failure := fmt.Errorf("failed")
	for _, tc := range []struct {
		name            string
		err             error
		experimentState model.ExperimentState
		runState        model.RunState
		message         string
	}{
		{"success", nil, model.ExperimentStateDone, model.RunStateDone, ""},
		{"canceled", fmt.Errorf("setup: %w", context.Canceled), model.ExperimentStateStopped, model.RunStateStopped, ""},
		{"deadline", context.DeadlineExceeded, model.ExperimentStateError, model.RunStateError, context.DeadlineExceeded.Error()},
		{"error", failure, model.ExperimentStateError, model.RunStateError, failure.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			experimentState, message := experimentStopReason(tc.err)
			if experimentState != tc.experimentState || message != tc.message {
				t.Errorf("experimentStopReason(%v) = %q, %q; want %q, %q", tc.err, experimentState, message, tc.experimentState, tc.message)
			}
			runState, message := runStopReason(tc.err)
			if runState != tc.runState || message != tc.message {
				t.Errorf("runStopReason(%v) = %q, %q; want %q, %q", tc.err, runState, message, tc.runState, tc.message)
			}
		})
	}
}

func TestExpandRunScript(t *testing.T) {
	point := model.Point{"foo": "a'b; {bar}", "bar": 42}
	got := expandRunScript(`{foo} {bar} {missing} {"result":1}`, point)
	want := `a'b; {bar} 42 {missing} {"result":1}`
	if got != want {
		t.Errorf("script = %q, want %q", got, want)
	}
}

func TestRootHelp(t *testing.T) {
	var help string
	for _, args := range [][]string{nil, {"-h"}, {"--help"}, {"help"}} {
		code, stdout, stderr := runCommand(context.Background(), args...)
		if code != 0 || stderr != "" {
			t.Fatalf("Main(%q) = %d, %q, %q", args, code, stdout, stderr)
		}
		if help == "" {
			help = stdout
		} else if stdout != help {
			t.Errorf("Main(%q) differs from default help", args)
		}
	}
	for _, section := range []string{"Study files:", "Scripts:", "Workflow:", "Results (next to the study file):", "--no-reuse", "DOE_EXPERIMENT_DIR", "report.log", `state = "done"`} {
		if !strings.Contains(help, section) {
			t.Errorf("help is missing %q", section)
		}
	}

	// Validate the documented study and preset without executing external scripts.
	_, example, ok := strings.Cut(help, "Example study.yaml:\n")
	if !ok {
		t.Fatal("help is missing the example study")
	}
	example, _, _ = strings.Cut(example, "\n\n")
	path := filepath.Join(t.TempDir(), "study.yaml")
	if err := os.WriteFile(path, []byte(example), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		preset     string
		points     int
		replicates int
	}{
		{"smoke", 2, 1},
		{"full", 4, 6},
	} {
		experiment, err := prepareExperiment(path, tc.preset, "", "", "", 0, nil)
		if err != nil {
			t.Fatalf("example preset %s: %v", tc.preset, err)
		}
		if len(experiment.Points) != tc.points || experiment.Replicates != tc.replicates {
			t.Errorf("example preset %s: got %d points, %d replicates; want %d, %d",
				tc.preset, len(experiment.Points), experiment.Replicates, tc.points, tc.replicates)
		}
	}
}

func TestExperimentHelpAndErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "run     Run an experiment"},
		{[]string{"run", "--help"}, "Usage: doe run"},
		{[]string{"run", "--help"}, "-c, --clean"},
	} {
		code, stdout, stderr := runCommand(context.Background(), tc.args...)
		if code != 0 || !strings.Contains(stdout, tc.want) || stderr != "" {
			t.Errorf("Main(%q) = %d, %q, %q", tc.args, code, stdout, stderr)
		}
	}
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"run", "foo=1"}, "run script is required"},
		{[]string{"run", "foo=1", "-r", "echo '{}'"}, "missing placeholder {foo}"},
		{[]string{"experiment"}, "unknown command: experiment"},
		{[]string{"unknown"}, "unknown command"},
	} {
		code, stdout, stderr := runCommand(context.Background(), tc.args...)
		if code != 1 || stdout != "" || !strings.Contains(stderr, tc.want) {
			t.Errorf("Main(%q) = %d, %q, %q; want %q", tc.args, code, stdout, stderr, tc.want)
		}
	}
	if _, err := os.Stat("results"); !os.IsNotExist(err) {
		t.Errorf("validation created results: %v", err)
	}
}

func TestRecordJSONIsNested(t *testing.T) {
	t.Chdir(t.TempDir())
	code, stdout, stderr := runCommand(context.Background(), "run", "foo=1", "-r", `echo '{"value":2}'; true '{foo}'`)
	if code != 0 || stderr != "" {
		t.Fatal(stderr)
	}
	experiment, runs := readCompletedExperiment(t, "results", stdout)
	data, err := os.ReadFile(filepath.Join("results", "experiments", experiment.ID.String(), "runs", runs[0].ID.String()+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record["point"] == nil || record["outcome"] == nil || record["foo"] != nil || record["value"] != nil || !bytes.HasSuffix(data, []byte("\n")) {
		t.Errorf("run JSON = %s", data)
	}
}
