package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/felixge/doe/internal/cli"
	"github.com/felixge/doe/internal/model"
)

func TestExecuteResumeAndForce(t *testing.T) {
	root := t.TempDir()
	designPath := filepath.Join(root, "design.yaml")
	writeFile(t, designPath, `setup: 'printf ''setup log\n{"host":"test"}\n'''
factors:
  - value: ['one two', "quote'it"]
run: 'printf ''run log\n''; printf ''{"seen":"%s"}\n'' {value}'
replicates: 2
`)

	firstOut, firstErr := newBuffers()
	if err := Execute(context.Background(), testEnv(firstOut, firstErr), Options{Designs: []string{designPath}}); err != nil {
		t.Fatal(err)
	}
	output, err := canonicalPath(filepath.Join(root, "results"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := firstOut.String(), output+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if firstErr.Len() != 0 {
		t.Fatalf("command output streamed to stderr: %q", firstErr.String())
	}
	if got := lineCount(t, filepath.Join(output, "experiments.jsonl")); got != 1 {
		t.Fatalf("experiment count = %d, want 1", got)
	}
	if got := lineCount(t, filepath.Join(output, "runs.jsonl")); got != 4 {
		t.Fatalf("run count = %d, want 4", got)
	}
	if _, err := os.Stat(filepath.Join(output, "study")); !os.IsNotExist(err) {
		t.Fatalf("results contains a study copy: %v", err)
	}

	if err := os.Mkdir(filepath.Join(root, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "work", "temporary.txt"), "ignored work")
	secondOut, secondErr := newBuffers()
	if err := Execute(context.Background(), testEnv(secondOut, secondErr), Options{Designs: []string{designPath}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(secondErr.String(), "run log") {
		t.Fatalf("resumed invocation executed a run:\n%s", secondErr.String())
	}
	if secondErr.Len() != 0 {
		t.Fatalf("setup output streamed to stderr: %q", secondErr.String())
	}
	if got := lineCount(t, filepath.Join(output, "experiments.jsonl")); got != 2 {
		t.Fatalf("experiment count = %d, want 2", got)
	}

	writeFile(t, designPath, `setup: 'printf ''{"host":"test"}\n'''
factors:
  - value: ['one two', "quote'it", three]
run: 'printf ''run log\n''; printf ''{"seen":"%s"}\n'' {value}'
replicates: 2
`)
	if err := Execute(context.Background(), testEnv(new(bytes.Buffer), new(bytes.Buffer)), Options{Designs: []string{designPath}}); err == nil || !strings.Contains(err.Error(), "--dirty") || !strings.Contains(err.Error(), "clear the results directory") {
		t.Fatalf("changed study error = %v", err)
	}
	dirtyOut, dirtyErr := newBuffers()
	if err := Execute(context.Background(), testEnv(dirtyOut, dirtyErr), Options{Designs: []string{designPath}, Dirty: true}); err != nil {
		t.Fatal(err)
	}
	if dirtyErr.Len() != 0 {
		t.Fatalf("command output streamed to stderr: %q", dirtyErr.String())
	}
	if got := lineCount(t, filepath.Join(output, "runs.jsonl")); got != 6 {
		t.Fatalf("run count = %d, want 6", got)
	}

	cleanOut, cleanErr := newBuffers()
	if err := Execute(context.Background(), testEnv(cleanOut, cleanErr), Options{Designs: []string{designPath}, Clean: true}); err != nil {
		t.Fatal(err)
	}
	if cleanErr.Len() != 0 {
		t.Fatalf("command output streamed to stderr: %q", cleanErr.String())
	}
	if got := lineCount(t, filepath.Join(output, "experiments.jsonl")); got != 1 {
		t.Fatalf("clean experiment count = %d, want 1", got)
	}
	if got := lineCount(t, filepath.Join(output, "runs.jsonl")); got != 6 {
		t.Fatalf("clean run count = %d, want 6", got)
	}
	if _, err := os.Stat(filepath.Join(root, "work")); !os.IsNotExist(err) {
		t.Fatalf("clean left work directory: %v", err)
	}
}

func TestExecuteConcurrencyLimitsGroupsAndResumes(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "concurrency.sh"), `#!/bin/sh
set -eu
group=$1 id=$2 limit=$3 barrier=$4
mkdir -p work/state
slot=
i=1
while [ "$i" -le "$limit" ]; do
  if mkdir "work/state/slot-$group-$i" 2>/dev/null; then slot="work/state/slot-$group-$i"; break; fi
  i=$((i+1))
done
if [ -z "$slot" ]; then touch "work/state/violation-$group-$id"; exit 1; fi
trap 'rmdir "$slot" 2>/dev/null || true' EXIT
: >"work/state/started-$group-$id"
i=0
while :; do
  set -- work/state/started-*
  if [ -e "$1" ] && [ "$#" -ge "$barrier" ]; then break; fi
  i=$((i+1)); [ "$i" -lt 100 ] || exit 2
  sleep 0.05
done
printf 'run log\n{"ok":true}\n'
`)
	designPath := filepath.Join(root, "design.yaml")
	writeFile(t, designPath, "factors:\n  - group: [a, b]\n    id: [1, 2, 3]\nrun: './concurrency.sh {group} {id} 2 4'\nreplicates: 2\nconcurrency: 2\nconcurrency_by: [group]\n")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stdout, stderr := newBuffers()
	env := testEnv(stdout, stderr)
	if err := Execute(ctx, env, Options{Designs: []string{designPath}}); err != nil {
		t.Fatal(err)
	}
	if matches, _ := filepath.Glob(filepath.Join(root, "work", "state", "violation-*")); len(matches) != 0 {
		t.Fatalf("per-group concurrency cap exceeded: %v", matches)
	}
	if got := lineCount(t, filepath.Join(root, "results", "runs.jsonl")); got != 12 {
		t.Fatalf("run count = %d, want 12", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("command output streamed to stderr: %q", stderr.String())
	}
	stderr.Reset()
	if err := Execute(ctx, env, Options{Designs: []string{designPath}}); err != nil {
		t.Fatal(err)
	}
	if got := lineCount(t, filepath.Join(root, "results", "runs.jsonl")); got != 12 {
		t.Fatalf("resume run count = %d, want 12", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("resume executed runs: %s", stderr)
	}
}

func TestExecuteConcurrencyUngroupedCap(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "concurrency.sh"), `#!/bin/sh
set -eu
id=$1
mkdir -p work/state
slot=
for i in 1 2; do
  if mkdir "work/state/slot-$i" 2>/dev/null; then slot="work/state/slot-$i"; break; fi
done
if [ -z "$slot" ]; then touch "work/state/violation-$id"; exit 1; fi
trap 'rmdir "$slot" 2>/dev/null || true' EXIT
: >"work/state/started-$id"
i=0
while :; do
  set -- work/state/started-*
  if [ -e "$1" ] && [ "$#" -ge 2 ]; then break; fi
  i=$((i+1)); [ "$i" -lt 100 ] || exit 2
  sleep 0.05
done
printf '{"ok":true}\n'
`)
	designPath := filepath.Join(root, "design.yaml")
	writeFile(t, designPath, "factors: [{id: [1, 2, 3, 4]}]\nrun: './concurrency.sh {id}'\nconcurrency: 2\n")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Execute(ctx, testEnv(new(bytes.Buffer), new(bytes.Buffer)), Options{Designs: []string{designPath}}); err != nil {
		t.Fatal(err)
	}
	if matches, _ := filepath.Glob(filepath.Join(root, "work", "state", "violation-*")); len(matches) != 0 {
		t.Fatalf("ungrouped concurrency cap exceeded: %v", matches)
	}
}

func TestExecuteConcurrencyFailureCancelsAndPreservesResults(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "failure.sh"), `#!/bin/sh
set -eu
group=$1 id=$2
mkdir -p work/state
: >"work/state/started-$group-$id"
case "$group:$id" in
  success:1) printf '{"ok":true}\n' ;;
  fail:1)
    i=0
    while [ ! -s results/runs.jsonl ] || [ ! -e work/state/active ]; do
      i=$((i+1)); [ "$i" -lt 100 ] || exit 2; sleep 0.05
    done
    exit 1 ;;
  active:*) touch work/state/active; sleep 30; printf '{"ok":true}\n' ;;
  *) touch "work/state/queued-$group-$id"; printf '{"ok":true}\n' ;;
esac
`)
	designPath := filepath.Join(root, "design.yaml")
	writeFile(t, designPath, "factors:\n  - group: [success, fail, active]\n    id: [1, 2]\nrun: './failure.sh {group} {id}'\nconcurrency: 1\nconcurrency_by: [group]\n")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := Execute(ctx, testEnv(new(bytes.Buffer), new(bytes.Buffer)), Options{Designs: []string{designPath}})
	if err == nil {
		t.Fatal("Execute() succeeded, want run failure")
	}
	if ctx.Err() != nil {
		t.Fatalf("active run was not canceled before the deadline: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "work", "state", "queued-fail-2")); !os.IsNotExist(err) {
		t.Fatalf("queued run was dispatched: %v", err)
	}
	if got := lineCount(t, filepath.Join(root, "results", "runs.jsonl")); got < 1 {
		t.Fatalf("saved successful runs = %d, want at least 1", got)
	}
}

func TestExecuteConcurrencyCanceled(t *testing.T) {
	for _, beforeStart := range []bool{true, false} {
		name := "during run"
		if beforeStart {
			name = "before start"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			designPath := filepath.Join(root, "design.yaml")
			writeFile(t, designPath, "factors: [{id: [1, 2, 3]}]\nrun: printf 'started\\nwaiting\\n'; sleep 30; echo '{}'\nconcurrency: 2\n")
			deadline, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			ctx, cancel := context.WithCancel(deadline)
			defer cancel()
			if beforeStart {
				cancel()
			}
			env := testEnv(new(bytes.Buffer), new(bytes.Buffer))
			if !beforeStart {
				time.AfterFunc(100*time.Millisecond, cancel)
			}
			err := Execute(ctx, env, Options{Designs: []string{designPath}})
			if err == nil || !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatalf("Execute() = %v, context = %v; want cancellation", err, ctx.Err())
			}
		})
	}
}

func TestExecutePersistsSetupAndRunLogs(t *testing.T) {
	root := t.TempDir()
	designPath := filepath.Join(root, "design.yaml")
	writeFile(t, designPath, "setup: printf 'setup out\\n'; printf 'setup err\\n' >&2; printf '{\"host\":\"test\"}\\n'\nfactors: [{value: [x]}]\nrun: printf 'run out\\n'; printf 'run err\\n' >&2; printf '{\"ok\":true}\\n'\n")
	stdout, stderr := newBuffers()
	if err := Execute(context.Background(), testEnv(stdout, stderr), Options{Designs: []string{designPath}}); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("command output streamed to stderr: %q", stderr.String())
	}
	experiment := readJSONLine(t, filepath.Join(root, "results", "experiments.jsonl"))
	run := readJSONLine(t, filepath.Join(root, "results", "runs.jsonl"))
	logDir := filepath.Join(root, "results", experiment["experiment_id"].(string))
	assertFileContains(t, filepath.Join(logDir, "setup.txt"), "setup out\n", "setup err\n", `{"host":"test"}`+"\n")
	assertFileContains(t, filepath.Join(logDir, run["run_id"].(string)+".txt"), "run out\n", "run err\n", `{"ok":true}`+"\n")
}

func TestExecuteRetainsFailedSetupLog(t *testing.T) {
	root := t.TempDir()
	designPath := filepath.Join(root, "design.yaml")
	writeFile(t, designPath, "setup: printf 'setup failed\\n'; exit 9\nfactors: [{value: [x]}]\nrun: echo '{}'\n")
	err := Execute(context.Background(), testEnv(new(bytes.Buffer), new(bytes.Buffer)), Options{Designs: []string{designPath}})
	if err == nil {
		t.Fatal("Execute() succeeded")
	}
	logs, globErr := filepath.Glob(filepath.Join(root, "results", "*", "setup.txt"))
	if globErr != nil || len(logs) != 1 {
		t.Fatalf("logs = %v, error = %v", logs, globErr)
	}
	assertFileContains(t, logs[0], "setup failed\n")
	if !strings.Contains(err.Error(), logs[0]) {
		t.Fatalf("error %q does not contain log path %q", err, logs[0])
	}
}

func TestExecuteRetainsFailedAndInterruptedRunLogs(t *testing.T) {
	for _, test := range []struct {
		name   string
		script string
		cancel bool
	}{
		{name: "failed", script: "printf 'failed output\\n'; exit 7"},
		{name: "interrupted", script: "printf 'interrupted output\\n'; sleep 30", cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			designPath := filepath.Join(root, "design.yaml")
			writeFile(t, designPath, "factors: [{value: [x]}]\nrun: \""+test.script+"\"\n")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.cancel {
				time.AfterFunc(100*time.Millisecond, cancel)
			}
			err := Execute(ctx, testEnv(new(bytes.Buffer), new(bytes.Buffer)), Options{Designs: []string{designPath}})
			if err == nil {
				t.Fatal("Execute() succeeded")
			}
			logs, globErr := filepath.Glob(filepath.Join(root, "results", "*", "*.txt"))
			if globErr != nil || len(logs) != 1 {
				t.Fatalf("logs = %v, error = %v", logs, globErr)
			}
			assertFileContains(t, logs[0], test.name+" output\n")
			if !strings.Contains(err.Error(), logs[0]) {
				t.Fatalf("error %q does not contain log path %q", err, logs[0])
			}
		})
	}
}

func TestExecuteSetupOnlyDiscardsOutputAndCreatesNoResults(t *testing.T) {
	root := t.TempDir()
	designPath := filepath.Join(root, "design.yaml")
	writeFile(t, designPath, "setup: printf 'setup output\\n'\nfactors: [{value: [x]}]\nrun: echo '{}'\n")
	stdout, stderr := newBuffers()
	if err := Execute(context.Background(), testEnv(stdout, stderr), Options{Designs: []string{designPath}, Setup: true}); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "results")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("results exists after setup-only: %v", err)
	}
}

func TestExecuteSetupUsesOnlyJSONFinalLineAsEnvironment(t *testing.T) {
	root := t.TempDir()
	designPath := filepath.Join(root, "design.yaml")
	writeFile(t, designPath, "setup: printf 'setup complete\\n'\nfactors: [{value: [x]}]\nrun: echo '{}'\n")
	stdout, stderr := newBuffers()
	if err := Execute(context.Background(), testEnv(stdout, stderr), Options{Designs: []string{designPath}}); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("setup output streamed to stderr: %q", stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "results", "experiments.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"env":{}`) {
		t.Fatalf("experiment = %s, want empty environment", data)
	}
}

func TestExecutePersistsNestedSetupEnvironment(t *testing.T) {
	root := t.TempDir()
	designPath := filepath.Join(root, "design.yaml")
	writeFile(t, designPath, `setup: 'printf ''{"host":{"name":"test"},"labels":["fast","local"]}\n'''
factors: [{value: [x]}]
run: echo '{}'
`)

	if err := Execute(context.Background(), testEnv(new(bytes.Buffer), new(bytes.Buffer)), Options{Designs: []string{designPath}}); err != nil {
		t.Fatal(err)
	}
	experiment := readJSONLine(t, filepath.Join(root, "results", "experiments.jsonl"))
	environment, ok := experiment["env"].(map[string]any)
	if !ok {
		t.Fatalf("env = %#v", experiment["env"])
	}
	host, ok := environment["host"].(map[string]any)
	if !ok || host["name"] != "test" {
		t.Fatalf("host = %#v", environment["host"])
	}
	labels, ok := environment["labels"].([]any)
	if !ok || len(labels) != 2 {
		t.Fatalf("labels = %#v", environment["labels"])
	}
}

func TestExecuteRejectsReservedResponseName(t *testing.T) {
	root := t.TempDir()
	designPath := filepath.Join(root, "design.yaml")
	writeFile(t, designPath, "factors: [{value: [x]}]\nrun: echo '{\"end\":1}'\n")

	err := Execute(context.Background(), testEnv(new(bytes.Buffer), new(bytes.Buffer)), Options{Designs: []string{designPath}})
	if err == nil || !strings.Contains(err.Error(), `response name "end" is reserved`) {
		t.Fatalf("Execute() error = %v, want reserved response error", err)
	}
}

func TestExecutePersistsNestedOutputs(t *testing.T) {
	root := t.TempDir()
	designPath := filepath.Join(root, "design.yaml")
	writeFile(t, designPath, `factors: [{value: [x]}]
run: 'printf ''{"summary":{"count":2},"samples":[{"t":0,"value":1},{"t":1,"value":3}]}\n'''
`)

	if err := Execute(context.Background(), testEnv(new(bytes.Buffer), new(bytes.Buffer)), Options{Designs: []string{designPath}}); err != nil {
		t.Fatal(err)
	}
	run := readJSONLine(t, filepath.Join(root, "results", "runs.jsonl"))
	summary, ok := run["summary"].(map[string]any)
	if !ok || summary["count"] != float64(2) {
		t.Fatalf("summary = %#v", run["summary"])
	}
	samples, ok := run["samples"].([]any)
	if !ok || len(samples) != 2 {
		t.Fatalf("samples = %#v", run["samples"])
	}
}

func TestExecutePlan(t *testing.T) {
	root := t.TempDir()
	designPath := filepath.Join(root, "design.yaml")
	writeFile(t, designPath, `factors:
  - a: [x, y]
    b: [1]
run: echo '{}'
replicates: 2
`)
	stdout, stderr := newBuffers()
	if err := Execute(context.Background(), testEnv(stdout, stderr), Options{Designs: []string{designPath}, Plan: true}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"| point | a | b |", "| #1    | x | 1 |", "| run/rep | 1  | 2  |", "| 2       | #2 | #1 |"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output does not contain %q:\n%s", want, stdout.String())
		}
	}
	if _, err := os.Stat(filepath.Join(root, "results")); !os.IsNotExist(err) {
		t.Fatalf("plan created results: %v", err)
	}
}

func TestCanonicalPath(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "study")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	want, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	got, err := canonicalPath(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("canonicalPath() = %q, want %q", got, want)
	}
	if _, err := canonicalPath(filepath.Join(real, "missing")); err == nil {
		t.Fatal("canonicalPath() succeeded for missing path")
	}
}

func TestLoadStudyRequiresSharedRoot(t *testing.T) {
	first := filepath.Join(t.TempDir(), "a.yaml")
	second := filepath.Join(t.TempDir(), "b.yaml")
	for _, path := range []string{first, second} {
		writeFile(t, path, "factors:\n  - x: [1]\nrun: echo '{}'\n")
	}
	_, err := loadStudy([]string{first, second})
	if err == nil || !strings.Contains(err.Error(), "same study root") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadStudyRejectsDesignSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.yaml")
	writeFile(t, target, "factors:\n  - x: [1]\nrun: echo '{}'\n")
	link := filepath.Join(root, "design.yaml")
	if err := os.Symlink("target.yaml", link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadStudy([]string{link}); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseObjectAllowsNestedValues(t *testing.T) {
	object, err := parseObject(`{"object":{"n":1},"array":[true,null]}`)
	if err != nil {
		t.Fatal(err)
	}
	nested, ok := object["object"].(map[string]any)
	if !ok {
		t.Fatalf("object type = %T", object["object"])
	}
	if _, ok := nested["n"].(json.Number); !ok {
		t.Fatalf("nested number type = %T", nested["n"])
	}
	for _, invalid := range []string{`[]`, `null`, `{"x":1} trailing`} {
		if _, err := parseObject(invalid); err == nil {
			t.Errorf("parseObject(%q) succeeded", invalid)
		}
	}
}

func TestCommandOutputLogsCombinedOutputAndParsesFinalStdout(t *testing.T) {
	var log bytes.Buffer
	last, err := commandOutput(context.Background(), strings.NewReader(""), t.TempDir(), `printf 'stdout log\n'; printf 'stderr log\n' >&2; printf '{"ok":true}\n'`, &log)
	if err != nil {
		t.Fatal(err)
	}
	if last != `{"ok":true}` {
		t.Fatalf("last = %q", last)
	}
	for _, want := range []string{"stdout log\n", "stderr log\n", "{\"ok\":true}\n"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log %q does not contain %q", log.String(), want)
		}
	}
}

func TestLoadResultsIndexesRunsByDuration(t *testing.T) {
	output := t.TempDir()
	writeFile(t, filepath.Join(output, "experiments.jsonl"), `{"experiment_id":"experiment","design":"design.yaml","factors":["value"]}`+"\n")
	writeFile(t, filepath.Join(output, "runs.jsonl"), `{"experiment_id":"experiment","replicate":1,"start":"2026-09-19T12:00:00Z","end":"2026-09-19T12:00:02.25Z","value":"one"}`+"\n")
	results, err := loadResults(output)
	if err != nil {
		t.Fatal(err)
	}
	key, err := reuseKey("design.yaml", 1, model.Point{Values: []model.Value{{Name: "value", Value: "one"}}})
	if err != nil {
		t.Fatal(err)
	}
	duration, ok := results.runs[key]
	if !ok {
		t.Fatal("run is not indexed")
	}
	if duration != 2250*time.Millisecond {
		t.Fatalf("duration = %v, want 2.25s", duration)
	}
}

func TestInterpolateDoesNotRescanValues(t *testing.T) {
	point := model.Point{Values: []model.Value{
		{Name: "a", Value: "{b}"},
		{Name: "b", Value: "; echo injected"},
	}}
	got, err := interpolate("printf '%s\\n' {a} {b}", point)
	if err != nil {
		t.Fatal(err)
	}
	want := `printf '%s\n' '{b}' '; echo injected'`
	if got != want {
		t.Fatalf("interpolate() = %q, want %q", got, want)
	}
}

func TestOutputSafety(t *testing.T) {
	unowned := filepath.Join(t.TempDir(), "output")
	if err := os.Mkdir(unowned, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(unowned, "important.txt"), "keep")
	if err := ensureOwnedOutput(unowned); err == nil {
		t.Fatal("ensureOwnedOutput accepted an unrelated nonempty directory")
	}

	owned := filepath.Join(t.TempDir(), "output")
	if err := os.Mkdir(owned, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(owned, ".doe"), "not doe\n")
	if err := ensureOwnedOutput(owned); err == nil {
		t.Fatal("ensureOwnedOutput accepted an invalid ownership marker")
	}
}

func testEnv(stdout, stderr *bytes.Buffer) *cli.Env {
	return &cli.Env{
		Stdin:  strings.NewReader(""),
		Stdout: stdout,
		Stderr: stderr,
	}
}

func newBuffers() (*bytes.Buffer, *bytes.Buffer) {
	return new(bytes.Buffer), new(bytes.Buffer)
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lineCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return len(bytes.FieldsFunc(data, func(r rune) bool { return r == '\n' }))
}

func readJSONLine(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func assertFileContains(t *testing.T, path string, values ...string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if !bytes.Contains(data, []byte(value)) {
			t.Errorf("%s = %q, want it to contain %q", path, data, value)
		}
	}
}
