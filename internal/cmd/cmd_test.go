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
	r := results.Open(dir)
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
	code, stdout, stderr := runCommand(context.Background(), "experiment", "-f", studyPath)
	if code != 0 || stderr != "" {
		t.Fatalf("experiment = %d, %q, %q", code, stdout, stderr)
	}
	experiment, runs := readCompletedExperiment(t, filepath.Join(dir, "results"), stdout)
	if experiment.State != model.StateDone || len(experiment.Points) != 6 || experiment.SetupEnd.Before(experiment.Start) || experiment.End.Before(experiment.SetupEnd) {
		t.Errorf("experiment = %+v", experiment)
	}
	if len(runs) != 6 {
		t.Fatalf("runs = %d, want 6", len(runs))
	}
	for _, run := range runs {
		foo := int(run.Point["foo"].(float64))
		bar := int(run.Point["bar"].(float64))
		result := int(run.Outcome["result"].(float64))
		if run.ExperimentID != experiment.ID || run.State != model.StateDone || result != foo+bar || run.End.Before(run.Start) {
			t.Errorf("run = %+v", run)
		}
		if _, err := os.Stat(filepath.Join(dir, "results", "runs", run.ID.String()+".log")); err != nil {
			t.Errorf("missing run log: %v", err)
		}
	}
	membership, err := os.ReadFile(filepath.Join(dir, "results", "experiments", experiment.ID.String(), "runs.ids"))
	if err != nil || bytes.Count(membership, []byte("\n")) != 6 {
		t.Errorf("membership = %q, %v", membership, err)
	}
}

func TestExperimentSetup(t *testing.T) {
	dir := t.TempDir()
	studyPath := filepath.Join(dir, "study.yaml")
	study := "factors: {foo: [1]}\nsetup: echo marker > marker; echo warning; echo '{\"os\":\"test\"}'\nrun: test -f marker; echo '{}'; true '{foo}'\n"
	if err := os.WriteFile(studyPath, []byte(study), 0600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCommand(context.Background(), "experiment", "-f", studyPath)
	if code != 0 || stderr != "" {
		t.Fatalf("experiment = %d, %q, %q", code, stdout, stderr)
	}
	experiment, _ := readCompletedExperiment(t, filepath.Join(dir, "results"), stdout)
	if !reflect.DeepEqual(experiment.Env, model.Env{"os": "test"}) || experiment.State != model.StateDone {
		t.Errorf("experiment = %+v", experiment)
	}
	log, err := os.ReadFile(filepath.Join(dir, "results", "experiments", experiment.ID.String(), "setup.log"))
	if err != nil || string(log) != "warning\n{\"os\":\"test\"}\n" {
		t.Errorf("setup log = %q, %v", log, err)
	}
}

func TestExperimentWithoutSetupCreatesEmptyLog(t *testing.T) {
	t.Chdir(t.TempDir())
	code, stdout, stderr := runCommand(context.Background(), "experiment", "foo=1", "-r", "echo '{}'; true '{foo}'")
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
		wantState model.State
		wantError string
	}{
		{"failure", func() context.Context { return context.Background() }, "echo failed; exit 7", 1, model.StateError, "setup: exit status 7"},
		{"canceled", func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }, "echo '{}'", 130, model.StateStopped, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			code, stdout, stderr := runCommand(tc.ctx(), "experiment", "foo=1", "-s", tc.script, "-r", "echo '{}'; true '{foo}'")
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
		code, stdout, stderr := runCommand(ctx, "experiment", "foo=1", "-s", "touch setup-started; exec sleep 30", "-r", "echo '{}'; true '{foo}'")
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
	if experiment.State != model.StateStopped || experiment.Error != "" || experiment.End.IsZero() || len(runs) != 0 {
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
	go func() {
		code, stdout, stderr := runCommand(ctx, "experiment", "foo=[1,2]", "-r",
			`if [ {foo} = 1 ]; then echo '{}'; else printf partial; touch started; while :; do sleep 0.01; done; fi`)
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
	if experiment.State != model.StateStopped || len(runs) != 2 || runs[0].State != model.StateDone || runs[1].State != model.StateStopped || !runs[1].End.After(runs[1].Start) {
		t.Errorf("experiment = %+v, runs = %+v", experiment, runs)
	}
	log, err := os.ReadFile(filepath.Join("results", "runs", runs[1].ID.String()+".log"))
	if err != nil || string(log) != "partial" {
		t.Errorf("interrupted log = %q, %v", log, err)
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
	code, stdout, stderr := runCommand(context.Background(), "experiment", "-f", path, "-p", "full")
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
replicates: 3
presets:
  full:
    factors: {foo: [3, 4]}
    replicates: 5
`
	if err := os.WriteFile(path, []byte(study), 0600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCommand(context.Background(), "experiment", "-f", path, "-p", "full", "foo=[7,8]", "-n", "2", "--design")
	if code != 0 || stderr != "" {
		t.Fatalf("design = %d, %q, %q", code, stdout, stderr)
	}
	var got model.Design
	if err := yaml.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	want := model.Design{Factors: model.Factors{"foo": {7, 8}, "bar": {true}}, Setup: "touch setup-ran", Run: "touch run-ran; true '{foo}' '{bar}'", Replicates: 2}
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
			code, stdout, _ := runCommand(context.Background(), "experiment", "foo=1", "-r", "true '{foo}'; "+tc.script)
			if code != 1 {
				t.Fatalf("experiment = %d, %q", code, stdout)
			}
			experiment, runs := readCompletedExperiment(t, "results", stdout)
			if experiment.State != model.StateError || len(runs) != 1 || runs[0].State != model.StateError || runs[0].Error == "" {
				t.Errorf("experiment = %+v, runs = %+v", experiment, runs)
			}
		})
	}
}

func TestNestedOutcomeAllowsFieldCollisions(t *testing.T) {
	t.Chdir(t.TempDir())
	code, stdout, stderr := runCommand(context.Background(), "experiment", "error=input", "-r", `echo '{"error":"measurement"}'; true '{error}'`)
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
	code, stdout, stderr := runCommand(context.Background(), "experiment", "foo=[1,2]", "-r", `echo '{"value":42}'; true '{foo}'; exit 7`)
	if code != 1 || !strings.Contains(stderr, "exit status 7") {
		t.Fatalf("experiment = %d, %q, %q", code, stdout, stderr)
	}
	experiment, runs := readCompletedExperiment(t, "results", stdout)
	if experiment.State != model.StateError || len(runs) != 1 || runs[0].State != model.StateError || runs[0].Outcome["value"] != float64(42) || !strings.Contains(runs[0].Error, "exit status 7") {
		t.Errorf("experiment = %+v, runs = %+v", experiment, runs)
	}
}

func TestRunOutcomeWithoutFinalNewline(t *testing.T) {
	t.Chdir(t.TempDir())
	code, stdout, stderr := runCommand(context.Background(), "experiment", "foo=1", "-r", `echo warning; true '{foo}'; printf '{"ok":true}'`)
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
		code, stdout, stderr := runCommand(context.Background(), "experiment", "foo=1", "-r", `touch started; while [ ! -f gate ]; do sleep 0.01; done; echo '{}'; true '{foo}'`)
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
	status, err := results.Open("results").Status()
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
		[]string{"experiment", "foo=1", "-r", `if read value; then echo read; else echo no-input; fi; echo '{}'; true '{foo}'`})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("experiment = %d, %q", code, &stderr)
	}
	_, runs := readCompletedExperiment(t, "results", stdout.String())
	log, err := os.ReadFile(filepath.Join("results", "runs", runs[0].ID.String()+".log"))
	if err != nil || string(log) != "no-input\n{}\n" {
		t.Errorf("log = %q, %v", log, err)
	}
}

func TestExperimentCleanPreservesLock(t *testing.T) {
	t.Chdir(t.TempDir())
	args := []string{"experiment", "foo=1", "-r", "echo '{}'; true '{foo}'"}
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
	if experiment.State != model.StateDone || len(runs) != 1 {
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
	experiment, err := prepareExperiment(path, "full", "", "echo CLI; true '{foo}' '{bar}' '{baz}'", 2, []string{"bar=[5,6]"})
	if err != nil {
		t.Fatal(err)
	}
	if experiment.Preset != "full" || experiment.Setup != "echo preset" || experiment.Replicates != 2 ||
		!reflect.DeepEqual(experiment.Factors, model.Factors{"foo": {3}, "bar": {5, 6}, "baz": {4}}) {
		t.Errorf("experiment = %+v", experiment)
	}
	if _, err := prepareExperiment(path, "missing", "", "", 0, nil); err == nil || !strings.Contains(err.Error(), "unknown preset") {
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
		name    string
		err     error
		state   model.State
		message string
	}{
		{"success", nil, model.StateDone, ""},
		{"canceled", fmt.Errorf("setup: %w", context.Canceled), model.StateStopped, ""},
		{"deadline", context.DeadlineExceeded, model.StateError, context.DeadlineExceeded.Error()},
		{"error", failure, model.StateError, failure.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, message := stopReason(tc.err)
			if state != tc.state || message != tc.message {
				t.Errorf("stopReason(%v) = %q, %q; want %q, %q", tc.err, state, message, tc.state, tc.message)
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

func TestExperimentHelpAndErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "experiment  Run an experiment"},
		{[]string{"experiment", "--help"}, "-c, --clean"},
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
		{[]string{"experiment", "foo=1"}, "run script is required"},
		{[]string{"experiment", "foo=1", "-r", "echo '{}'"}, "missing placeholder {foo}"},
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
	code, stdout, stderr := runCommand(context.Background(), "experiment", "foo=1", "-r", `echo '{"value":2}'; true '{foo}'`)
	if code != 0 || stderr != "" {
		t.Fatal(stderr)
	}
	_, runs := readCompletedExperiment(t, "results", stdout)
	data, err := os.ReadFile(filepath.Join("results", "runs", runs[0].ID.String()+".json"))
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
