package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/felixge/doe/internal/cli"
	runcmd "github.com/felixge/doe/internal/cmd/run"
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
	if err := Execute(context.Background(), testEnv(firstOut, firstErr), runcmd.Options{Designs: []string{designPath}}); err != nil {
		t.Fatal(err)
	}
	output, err := canonicalPath(filepath.Join(root, "results"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := firstOut.String(), output+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := strings.Count(firstErr.String(), "run log"); got != 4 {
		t.Fatalf("run log count = %d, want 4; stderr:\n%s", got, firstErr.String())
	}
	if got := lineCount(t, filepath.Join(output, "experiments.jsonl")); got != 1 {
		t.Fatalf("experiment count = %d, want 1", got)
	}
	if got := lineCount(t, filepath.Join(output, "runs.jsonl")); got != 4 {
		t.Fatalf("run count = %d, want 4", got)
	}

	secondOut, secondErr := newBuffers()
	if err := Execute(context.Background(), testEnv(secondOut, secondErr), runcmd.Options{Designs: []string{designPath}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(secondErr.String(), "run log") {
		t.Fatalf("resumed invocation executed a run:\n%s", secondErr.String())
	}
	if got := secondErr.String(); got != "setup log\n" {
		t.Fatalf("stderr = %q, want only command output when it is not a TTY", got)
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
	if err := Execute(context.Background(), testEnv(new(bytes.Buffer), new(bytes.Buffer)), runcmd.Options{Designs: []string{designPath}}); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("changed study error = %v", err)
	}
	forcedOut, forcedErr := newBuffers()
	if err := Execute(context.Background(), testEnv(forcedOut, forcedErr), runcmd.Options{Designs: []string{designPath}, Force: true}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(forcedErr.String(), "run log"); got != 2 {
		t.Fatalf("forced new run count = %d, want 2; stderr:\n%s", got, forcedErr.String())
	}
	if got := lineCount(t, filepath.Join(output, "runs.jsonl")); got != 6 {
		t.Fatalf("run count = %d, want 6", got)
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
	if err := Execute(context.Background(), testEnv(stdout, stderr), runcmd.Options{Designs: []string{designPath}, Plan: true}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"| # | a | b |", "| 1 | x | 1 |", "| replicate | 1 | 2 |", "| 2         | 2 | 1 |"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output does not contain %q:\n%s", want, stdout.String())
		}
	}
	if _, err := os.Stat(filepath.Join(root, "results")); !os.IsNotExist(err) {
		t.Fatalf("plan created results: %v", err)
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

func TestParseFlatObject(t *testing.T) {
	object, err := parseFlatObject(`{"s":"x","n":1,"b":true,"z":null}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := object["n"].(json.Number); !ok {
		t.Fatalf("number type = %T", object["n"])
	}
	for _, invalid := range []string{`[]`, `{"nested":{}}`, `{"array":[]}`, `{"x":1} trailing`} {
		if _, err := parseFlatObject(invalid); err == nil {
			t.Errorf("parseFlatObject(%q) succeeded", invalid)
		}
	}
}

func TestCommandOutputStreamsEarlierLines(t *testing.T) {
	stdout, stderr := newBuffers()
	last, err := commandOutput(context.Background(), testEnv(stdout, stderr), t.TempDir(), `printf 'first\n{"ok":true}\n'`)
	if err != nil {
		t.Fatal(err)
	}
	if last != `{"ok":true}` || stderr.String() != "first\n" {
		t.Fatalf("last = %q, stderr = %q", last, stderr.String())
	}
}

func TestCommandOutputOnlyClearsProgressForLogs(t *testing.T) {
	for _, test := range []struct {
		name        string
		script      string
		wantCleared bool
	}{
		{name: "quiet", script: `printf '{"ok":true}\n'`},
		{name: "logging", script: `printf 'log\n{"ok":true}\n'`, wantCleared: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			progress := &progressBar{output: &stderr, writer: &stderr, total: 1, shown: true}
			env := testEnv(new(bytes.Buffer), &stderr)
			env.Stderr = progress
			if _, err := commandOutput(context.Background(), env, t.TempDir(), test.script); err != nil {
				t.Fatal(err)
			}
			if got := !progress.shown; got != test.wantCleared {
				t.Fatalf("progress cleared = %v, want %v; stderr = %q", got, test.wantCleared, stderr.String())
			}
		})
	}
}

func TestLoadResultsLoadsRunDurations(t *testing.T) {
	output := t.TempDir()
	writeFile(t, filepath.Join(output, "experiments.jsonl"), `{"experiment_id":"experiment","design":"design.yaml","factors":["value"]}`+"\n")
	writeFile(t, filepath.Join(output, "runs.jsonl"), `{"experiment_id":"experiment","replicate":1,"start":"2026-09-19T12:00:00Z","end":"2026-09-19T12:00:02.25Z","value":"one"}`+"\n")
	results, err := loadResults(output, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, err := reuseKey("design.yaml", 1, model.Point{Values: []model.Value{{Name: "value", Value: "one"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := results.durations[key]; got != 2250*time.Millisecond {
		t.Fatalf("duration = %v, want 2.25s", got)
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
	study := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(outside, "out")
	if err := os.Symlink(filepath.Dir(study), link); err != nil {
		t.Fatal(err)
	}
	resolved, err := canonicalPath(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOutput(study, resolved); err == nil {
		t.Fatal("validateOutput accepted a symlink resolving to an ancestor of the study")
	}

	unowned := filepath.Join(t.TempDir(), "output")
	if err := os.Mkdir(unowned, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(unowned, "important.txt"), "keep")
	if _, err := lockResults(unowned); err == nil {
		t.Fatal("lockResults accepted an unrelated nonempty directory")
	}

	owned := filepath.Join(t.TempDir(), "output")
	if err := os.Mkdir(owned, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(owned, ".doe"), "not doe\n")
	if _, err := lockResults(owned); err == nil {
		t.Fatal("lockResults accepted an invalid ownership marker")
	}
}

func testEnv(stdout, stderr *bytes.Buffer) *cli.Env {
	return &cli.Env{
		Stdin:  strings.NewReader(""),
		Stdout: stdout,
		Stderr: stderr,
		Readme: []byte("# doe\n\n`go install example/doe@latest`\n"),
	}
}

func newBuffers() (*bytes.Buffer, *bytes.Buffer) {
	return new(bytes.Buffer), new(bytes.Buffer)
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
