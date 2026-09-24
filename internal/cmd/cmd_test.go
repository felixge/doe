package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/felixge/doe2/internal/cli"
	"github.com/felixge/doe2/internal/model"
	"github.com/felixge/doe2/internal/results"
	"uuid"
)

func TestExperimentIntegration(t *testing.T) {
	t.Chdir(t.TempDir())
	path := filepath.Join(t.TempDir(), "study.yaml")
	study := `factors:
  foo: [1, 2, 3]
  bar: [4, 5]
run: |
  printf '{"result":%s}\n' "$(({foo} + {bar}))"
`
	if err := os.WriteFile(path, []byte(study), 0600); err != nil {
		t.Fatal(err)
	}
	recorded := make(map[string]model.Results)
	seenLogs := make(map[string]map[string]bool)
	for _, tc := range []struct {
		name string
		args []string
		want [][2]int
	}{
		{"file", []string{"experiment", "-f", path}, [][2]int{{1, 4}, {2, 4}, {3, 4}, {1, 5}, {2, 5}, {3, 5}}},
		{"override", []string{"experiment", "-f", path, "foo=9"}, [][2]int{{9, 4}, {9, 5}}},
		{"empty run flag", []string{"experiment", "-f", path, "-r", ""}, [][2]int{{1, 4}, {2, 4}, {3, 4}, {1, 5}, {2, 5}, {3, 5}}},
		{"flags and factors interspersed", []string{"experiment", "foo=[1, 2, 3]", "bar=[4, 5]", "-r", `printf '{"result":%s}\n' "$(({foo} + {bar}))"`}, [][2]int{{1, 4}, {2, 4}, {3, 4}, {1, 5}, {2, 5}, {3, 5}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			env := &cli.Env{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}
			if code := Main(context.Background(), env, tc.args); code != 0 {
				t.Fatalf("exit code %d: %s", code, &stderr)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q", &stderr)
			}
			resultsDir := "."
			if tc.name != "flags and factors interspersed" {
				resultsDir = filepath.Dir(path)
				if _, err := os.Stat("results"); !os.IsNotExist(err) {
					t.Errorf("study experiment wrote results in current directory: %v", err)
				}
			}
			data, err := os.ReadFile(filepath.Join(resultsDir, "results", "experiments.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			records := strings.Split(strings.TrimSpace(string(data)), "\n")
			previous := recorded[resultsDir]
			if len(records) != len(previous.Experiments)+1 {
				t.Fatalf("got %d experiment records, want %d", len(records), len(previous.Experiments)+1)
			}
			var experiment model.Experiment
			if err := json.Unmarshal([]byte(records[len(records)-1]), &experiment); err != nil {
				t.Fatal(err)
			}
			if experiment.ID == (model.Experiment{}).ID || len(experiment.Factors) != 2 || experiment.Run == "" {
				t.Errorf("invalid experiment record: %+v", experiment)
			}
			if want := experiment.ID.String() + "\n"; stdout.String() != want {
				t.Errorf("stdout = %q, want %q", &stdout, want)
			}
			for _, prior := range previous.Experiments {
				if experiment.ID == prior.ID {
					t.Errorf("reused experiment ID: %s", experiment.ID)
				}
			}
			if len(experiment.Factors["foo"]) != len(tc.want)/len(experiment.Factors["bar"]) {
				t.Errorf("recorded factors = %v, want design for %v", experiment.Factors, tc.want)
			}
			runData, err := os.ReadFile(filepath.Join(resultsDir, "results", "runs.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			runLines := strings.Split(strings.TrimSpace(string(runData)), "\n")
			if len(runLines) != len(seenLogs[resultsDir])+len(tc.want) {
				t.Fatalf("got %d run records, want %d", len(runLines), len(seenLogs[resultsDir])+len(tc.want))
			}
			for _, line := range runLines[len(runLines)-len(tc.want):] {
				var got struct {
					ID     uuid.UUID `json:"id"`
					Foo    int       `json:"foo"`
					Bar    int       `json:"bar"`
					Result int       `json:"result"`
				}
				if err := json.Unmarshal([]byte(line), &got); err != nil {
					t.Fatal(err)
				}
				if got.ID == (uuid.UUID{}) || got.Result != got.Foo+got.Bar {
					t.Errorf("invalid run record: %+v", got)
				}
			}
			logs, err := filepath.Glob(filepath.Join(resultsDir, "results", "runs", "*.log"))
			if err != nil {
				t.Fatal(err)
			}
			if len(logs) != len(seenLogs[resultsDir])+len(tc.want) {
				t.Fatalf("got %d run logs, want %d", len(logs), len(seenLogs[resultsDir])+len(tc.want))
			}
			if seenLogs[resultsDir] == nil {
				seenLogs[resultsDir] = make(map[string]bool)
			}
			counts := make(map[int]int)
			for _, path := range logs {
				if seenLogs[resultsDir][path] {
					continue
				}
				seenLogs[resultsDir][path] = true
				if _, err := uuid.Parse(strings.TrimSuffix(filepath.Base(path), ".log")); err != nil {
					t.Errorf("invalid run log name %q: %v", path, err)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var got struct {
					Result int `json:"result"`
				}
				if err := json.Unmarshal(data, &got); err != nil {
					t.Fatal(err)
				}
				counts[got.Result]++
			}
			for _, want := range tc.want {
				if counts[want[0]+want[1]] == 0 {
					t.Errorf("missing result for point %v", want)
				}
				counts[want[0]+want[1]]--
			}
			lock, err := os.ReadFile(filepath.Join(resultsDir, "results", "experiment.lock"))
			if err != nil || strings.TrimSpace(string(lock)) != experiment.ID.String() {
				t.Errorf("lock contains %q, %v; want %s", lock, err, experiment.ID)
			}
			previous.Experiments = append(previous.Experiments, experiment)
			recorded[resultsDir] = previous
		})
	}
}

func TestExperimentReplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "study.yaml")
	study := `factors:
  foo: [1, 2]
replicates: 3
run: echo '{"ok":true}'
`
	if err := os.WriteFile(path, []byte(study), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Main(context.Background(), &cli.Env{Stdout: &stdout, Stderr: &stderr}, []string{"experiment", "-f", path}); code != 0 {
		t.Fatalf("exit code %d: %s", code, &stderr)
	}
	dir := filepath.Join(filepath.Dir(path), "results")
	experiments, err := os.ReadFile(filepath.Join(dir, "experiments.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var experiment model.Experiment
	if err := json.Unmarshal(bytes.TrimSpace(experiments), &experiment); err != nil {
		t.Fatal(err)
	}
	if got := experiment.ReplicateCount(); got != 3 {
		t.Errorf("recorded replicates = %d, want 3", got)
	}
	runs, err := os.ReadFile(filepath.Join(dir, "runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(runs), []byte("\n"))
	if len(lines) != 6 {
		t.Fatalf("run count = %d, want 6", len(lines))
	}
	counts := make(map[int]int)
	ids := make(map[uuid.UUID]bool)
	for _, line := range lines {
		var run struct {
			ID  uuid.UUID `json:"id"`
			Foo int       `json:"foo"`
			OK  bool      `json:"ok"`
		}
		if err := json.Unmarshal(line, &run); err != nil {
			t.Fatal(err)
		}
		if run.ID == (uuid.UUID{}) || ids[run.ID] || !run.OK {
			t.Errorf("invalid replicated run: %+v", run)
		}
		ids[run.ID] = true
		counts[run.Foo]++
		if _, err := os.Stat(filepath.Join(dir, "runs", run.ID.String()+".log")); err != nil {
			t.Errorf("missing log for run %s: %v", run.ID, err)
		}
	}
	if counts[1] != 3 || counts[2] != 3 {
		t.Errorf("runs by point = %v, want 3 each", counts)
	}
}

func TestExperimentInvalidReplicates(t *testing.T) {
	for _, value := range []string{"0", "-1", "1.5", "many"} {
		t.Run(value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "study.yaml")
			study := "factors:\n  foo: 1\nrun: echo '{}'\nreplicates: " + value + "\n"
			if err := os.WriteFile(path, []byte(study), 0600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			code := Main(context.Background(), &cli.Env{Stdout: &stdout, Stderr: &stderr}, []string{"experiment", "-f", path})
			if code != 1 || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Errorf("invalid replicates %s: exit code %d, stdout %q, stderr %q", value, code, &stdout, &stderr)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), "results")); !os.IsNotExist(err) {
				t.Errorf("invalid replicates created results: %v", err)
			}
		})
	}
}

func TestExperimentRecordedWhileRunning(t *testing.T) {
	t.Chdir(t.TempDir())
	var stdout, stderr bytes.Buffer
	env := &cli.Env{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}
	done := make(chan int, 1)
	var experiment model.Experiment
	go func() {
		done <- Main(context.Background(), env, []string{"experiment", "foo=1", "-r", `while [ ! -f gate ]; do sleep 0.01; done; echo '{}'`})
	}()
	defer func() {
		if err := os.WriteFile("gate", nil, 0600); err != nil {
			t.Error(err)
		}
		if code := <-done; code != 0 {
			t.Errorf("exit code %d: %s", code, &stderr)
		}
		if want := experiment.ID.String() + "\n"; stdout.String() != want {
			t.Errorf("stdout = %q, want %q", &stdout, want)
		}
	}()

	path := filepath.Join("results", "experiments.jsonl")
	var data []byte
	deadline := time.After(5 * time.Second)
	for len(data) == 0 {
		select {
		case <-deadline:
			t.Fatal("experiment was not recorded while running")
		case <-time.After(10 * time.Millisecond):
			data, _ = os.ReadFile(path)
		}
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &experiment); err != nil {
		t.Fatal(err)
	}
	var otherStderr bytes.Buffer
	otherEnv := &cli.Env{Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}, Stderr: &otherStderr}
	if code := Main(context.Background(), otherEnv, []string{"experiment", "foo=2", "-r", `echo '{}'`}); code != 1 || !strings.Contains(otherStderr.String(), "another experiment is running: "+experiment.ID.String()) {
		t.Errorf("concurrent experiment = code %d, stderr %q; want running experiment ID", code, &otherStderr)
	}
}

func TestRunDoesNotReadCLIStdin(t *testing.T) {
	t.Chdir(t.TempDir())
	var stdout, stderr bytes.Buffer
	code := Main(context.Background(), &cli.Env{Stdin: strings.NewReader("injected\n"), Stdout: &stdout, Stderr: &stderr},
		[]string{"experiment", "foo=1", "-r", `if read value; then printf 'read %s\n' "$value"; else printf 'no input\n'; fi; echo '{}'`})
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, &stderr)
	}
	logs, err := filepath.Glob(filepath.Join("results", "runs", "*.log"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("run logs = %v, %v; want one", logs, err)
	}
	data, err := os.ReadFile(logs[0])
	if err != nil || string(data) != "no input\n{}\n" {
		t.Errorf("run log = %q, %v; want no input", data, err)
	}
}

func TestRunOutcomeErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
	}{
		{"empty log", `:`},
		{"non-JSON", `echo 'not JSON'`},
		{"blank last line", `printf '{}\n\n'`},
		{"malformed JSON", `echo '{broken}'`},
		{"null", `echo null`},
		{"array", `echo '[1]'`},
		{"number", `echo 42`},
		{"conflicting response", `echo '{"foo":2}'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			var stdout, stderr bytes.Buffer
			code := Main(context.Background(), &cli.Env{Stdout: &stdout, Stderr: &stderr},
				[]string{"experiment", "foo=1", "-r", tc.script})
			if code != 1 || stdout.Len() != 0 {
				t.Errorf("exit code %d, stdout %q, stderr %q; want error", code, &stdout, &stderr)
			}
			data, err := os.ReadFile(filepath.Join("results", "runs.jsonl"))
			if err != nil && !os.IsNotExist(err) || len(data) != 0 {
				t.Errorf("invalid run was recorded: %q, %v", data, err)
			}
		})
	}
}

func TestRunOutcomeWithoutFinalNewline(t *testing.T) {
	t.Chdir(t.TempDir())
	var stdout, stderr bytes.Buffer
	code := Main(context.Background(), &cli.Env{Stdout: &stdout, Stderr: &stderr},
		[]string{"experiment", "foo=1", "-r", `echo warning >&2; printf '{"ok":true}'`})
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, &stderr)
	}
	data, err := os.ReadFile(filepath.Join("results", "runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Foo int  `json:"foo"`
		OK  bool `json:"ok"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &got); err != nil || got.Foo != 1 || !got.OK {
		t.Errorf("run record %q: %v", data, err)
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

func TestExperimentHelp(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "experiment  Run an experiment"},
		{[]string{"experiment", "--help"}, "Usage: doe experiment"},
	} {
		var stdout, stderr bytes.Buffer
		code := Main(context.Background(), &cli.Env{Stdout: &stdout, Stderr: &stderr}, tc.args)
		if code != 0 || !strings.Contains(stdout.String(), tc.want) || stderr.Len() != 0 {
			t.Errorf("Main(%q) = %d, stdout %q, stderr %q; want %q", tc.args, code, &stdout, &stderr, tc.want)
		}
	}
}

func TestExperimentErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"experiment", "foo=1"}, "run script is required"},
		{[]string{"experiment", "foo=1", "--run", "printf 'partial\\n'; printf 'warning\\n' >&2; exit 7"}, "exit status 7"},
		{[]string{"execute"}, "unknown command"},
		{[]string{"run"}, "unknown command"},
		{[]string{"results"}, "unknown command"},
	} {
		var stdout, stderr bytes.Buffer
		code := Main(context.Background(), &cli.Env{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}, tc.args)
		if code != 1 || !strings.Contains(stderr.String(), tc.want) || stdout.Len() != 0 {
			t.Errorf("Main(%q) = %d, stdout %q, stderr %q; want error %q and empty stdout", tc.args, code, &stdout, &stderr, tc.want)
		}
	}
	data, err := os.ReadFile(filepath.Join("results", "experiments.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d records, want one failed experiment", len(lines))
	}
	r, err := results.New("results")
	if err != nil {
		t.Fatal(err)
	}
	logs, err := filepath.Glob(filepath.Join("results", "runs", "*.log"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("run logs = %v, %v; want one failed run log", logs, err)
	}
	if output, err := os.ReadFile(logs[0]); err != nil || string(output) != "partial\nwarning\n" {
		t.Errorf("failed run log = %q, %v; want partial stdout and stderr", output, err)
	}
	for _, line := range lines {
		var experiment model.Experiment
		if err := json.Unmarshal([]byte(line), &experiment); err != nil {
			t.Fatal(err)
		}
		release, err := r.LockExperiment(experiment.ID)
		if err != nil {
			t.Errorf("failed experiment left lock held: %v", err)
			continue
		}
		if err := release(); err != nil {
			t.Error(err)
		}
	}
}

func TestExperimentRecordError(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("results", []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Main(context.Background(), &cli.Env{Stdout: &stdout, Stderr: &stderr},
		[]string{"experiment", "foo=1", "--run", `echo '{"result":1}'`})
	if code != 1 || !strings.Contains(stderr.String(), "create results directory") {
		t.Errorf("Main = %d, stderr %q; want results directory error", code, &stderr)
	}
}
