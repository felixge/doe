package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/felixge/doe/internal/cli"
	"github.com/felixge/doe/internal/model"
)

const overlappingStudy = `setup: mkdir -p work; echo setup >> work/order; echo '{"host":{"name":"test"},"labels":["local"]}'
run: printf '%s\n' {value} >> work/order; printf '{"seen":"%s"}\n' {value}
designs:
  smoke:
    factors: {value: ['one two', "quote'it"]}
  full:
    factors: {value: ['one two', "quote'it", three]}
    replicates: 2
    concurrency: 2
`

func TestExecuteReuseAcrossDesignsAndResume(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "compression.study.yaml"), overlappingStudy)
	output := filepath.Join(root, "results", "compression")
	run := func(selectors ...string) {
		t.Helper()
		stdout, stderr := newBuffers()
		if err := Execute(context.Background(), testEnv(stdout, stderr), Options{Project: root, Designs: selectors}); err != nil {
			t.Fatal(err)
		}
		if stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
		}
	}
	run("smoke")
	original, err := os.ReadFile(filepath.Join(output, "runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	first := readRecords[model.Experiment](t, filepath.Join(output, "experiments.jsonl"))[0]
	run("full")
	run("compression/smoke", "full")
	runs := readRecords[map[string]any](t, filepath.Join(output, "runs.jsonl"))
	if len(runs) != 6 {
		t.Fatalf("runs=%d, want 6", len(runs))
	}
	for i, value := range []string{"one two", "quote'it"} {
		if runs[i]["experiment_id"] != first.ID || runs[i]["value"] != value || runs[i]["seen"] != value {
			t.Fatalf("original run lost provenance or shell escaping: %v", runs[i])
		}
	}
	data, err := os.ReadFile(filepath.Join(output, "runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, original) {
		t.Fatal("reused records changed")
	}
	experiments := readRecords[model.Experiment](t, filepath.Join(output, "experiments.jsonl"))
	if len(experiments) != 3 || !reflect.DeepEqual(experiments[2].Designs, []string{"smoke", "full"}) {
		t.Fatalf("experiments=%+v", experiments)
	}
	for _, e := range experiments {
		if e.Study != "compression" || e.Env["host"].(map[string]any)["name"] != "test" {
			t.Fatalf("experiment=%+v", e)
		}
		assertFileContains(t, filepath.Join(output, e.ID, "setup.txt"), `"host"`)
	}
	if got := lineCount(t, filepath.Join(root, "work", "order")); got != 9 {
		t.Fatalf("setup/run invocations=%d, want 9", got)
	}
}

func TestExecuteReusesEarlierDesignWithReorderedFactors(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.study.yaml"), `setup: mkdir -p work; echo setup >> work/order
run: echo run >> work/order; echo '{}'
designs:
  smoke:
    factors: {a: [1], b: [2]}
  full:
    factors: {b: [2], a: [1, 3]}
    replicates: 2
    concurrency: 2
`)
	if err := Execute(context.Background(), testEnv(new(bytes.Buffer), new(bytes.Buffer)), Options{Project: root, Designs: []string{"smoke", "full"}}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "results", "a")
	experiments := readRecords[model.Experiment](t, filepath.Join(output, "experiments.jsonl"))
	if len(experiments) != 1 || !reflect.DeepEqual(experiments[0].Designs, []string{"smoke", "full"}) {
		t.Fatalf("experiments=%+v", experiments)
	}
	runs := readRecords[map[string]any](t, filepath.Join(output, "runs.jsonl"))
	if len(runs) != 4 || lineCount(t, filepath.Join(root, "work", "order")) != 5 {
		t.Fatalf("runs=%v; wanted four runs and one setup", runs)
	}
	seen := map[string]bool{}
	for _, run := range runs {
		key := fmt.Sprint(run["a"], "/", run["b"], "/", run["replicate"])
		if seen[key] || run["experiment_id"] != experiments[0].ID {
			t.Fatalf("duplicate or wrong provenance: %v", run)
		}
		seen[key] = true
	}
}

func TestExecuteInterleavedStudies(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		writeFile(t, filepath.Join(root, name+".study.yaml"), fmt.Sprintf(`setup: mkdir -p work; echo setup-%s >> work/order; echo '{"study":"%s"}'
run: echo %s-{value} >> work/order; echo '{}'
designs:
  x: {factors: {value: [1]}}
  y: {factors: {value: [2]}}
`, name, name, name))
	}
	err := Execute(context.Background(), testEnv(new(bytes.Buffer), new(bytes.Buffer)), Options{Project: root, Designs: []string{"a/y", "b/x", "a/x"}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "work", "order"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "setup-a\na-2\nsetup-b\nb-1\na-1\n" {
		t.Fatalf("order=%q", data)
	}
	for _, name := range []string{"a", "b"} {
		output := filepath.Join(root, "results", name)
		experiments := readRecords[model.Experiment](t, filepath.Join(output, "experiments.jsonl"))
		if len(experiments) != 1 {
			t.Fatalf("%s experiments=%+v", name, experiments)
		}
		e := experiments[0]
		if _, ok := e.Files[name+".study.yaml"]; !ok {
			t.Fatal("selected study missing from snapshot")
		}
		if len(e.Files) != 1 {
			t.Fatalf("snapshot includes other studies or work: %v", e.Files)
		}
		want := 1
		if name == "a" {
			want = 2
		}
		if got := lineCount(t, filepath.Join(output, "runs.jsonl")); got != want {
			t.Fatalf("%s runs=%d want %d", name, got, want)
		}
	}
}

func TestExecutePreflightHasNoSideEffects(t *testing.T) {
	for _, kind := range []string{"unknown", "duplicate", "ambiguous", "missing", "invalid study", "dirty later study", "snapshot symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"a", "b"} {
				writeFile(t, filepath.Join(root, name+".study.yaml"), overlappingStudy)
			}
			selectors := []string{"a/smoke", "b/full"}
			var partial string
			switch kind {
			case "unknown":
				selectors[1] = "wat"
			case "duplicate":
				selectors[1] = "a/smoke"
			case "ambiguous":
				selectors[1] = "full"
			case "missing":
				selectors = nil
			case "invalid study":
				writeFile(t, filepath.Join(root, "c.study.yaml"), "invalid")
			case "snapshot symlink":
				if err := os.Symlink("missing", filepath.Join(root, "input")); err != nil {
					t.Fatal(err)
				}
			case "dirty later study":
				output := filepath.Join(root, "results", "b")
				if err := os.MkdirAll(output, 0o755); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(output, ".doe"), resultsMarker)
				partial = filepath.Join(output, "experiments.jsonl")
				writeFile(t, partial, "{\"experiment_id\":\"previous\",\"files_hash\":\"changed\"}\n{\"partial\":")
			}
			for _, plan := range []bool{false, true} {
				err := Execute(context.Background(), testEnv(new(bytes.Buffer), new(bytes.Buffer)), Options{Project: root, Designs: selectors, Plan: plan})
				if err == nil {
					t.Fatal("expected preflight error")
				}
				for _, path := range []string{"work", "results/a"} {
					if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
						t.Fatalf("preflight created %s", path)
					}
				}
				if partial != "" {
					assertFileContains(t, partial, `{"partial":`)
				}
			}
		})
	}
}

func TestDirtyStateIsPerStudy(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		writeFile(t, filepath.Join(root, name+".study.yaml"), overlappingStudy)
	}
	opts := Options{Project: root, Designs: []string{"a/smoke"}}
	execute := func() error {
		return Execute(context.Background(), testEnv(new(bytes.Buffer), new(bytes.Buffer)), opts)
	}
	if err := execute(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "b.study.yaml"), overlappingStudy+"# changed other study\n")
	if err := execute(); err != nil {
		t.Fatalf("other study dirtied a: %v", err)
	}
	writeFile(t, filepath.Join(root, "a.study.yaml"), overlappingStudy+"# changed selected study\n")
	if err := execute(); err == nil || !strings.Contains(err.Error(), "--dirty") {
		t.Fatalf("error=%v", err)
	}
	opts.Dirty = true
	if err := execute(); err != nil {
		t.Fatal(err)
	}
	if got := lineCount(t, filepath.Join(root, "results", "a", "runs.jsonl")); got != 2 {
		t.Fatalf("runs=%d", got)
	}
	writeFile(t, filepath.Join(root, "shared.txt"), "shared input")
	opts.Dirty = false
	if err := execute(); err == nil {
		t.Fatal("shared input did not dirty a")
	}
}

func TestExecuteFailFastRetainsLogsAndResults(t *testing.T) {
	for _, setupFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(setupFailure), func(t *testing.T) {
			root := t.TempDir()
			setup := "mkdir -p work; echo setup"
			if setupFailure {
				setup += "; exit 9"
			}
			writeFile(t, filepath.Join(root, "a.study.yaml"), "setup: "+setup+"\nrun: echo run; [ {value} != fail ] || exit 7; echo '{}'\ndesigns:\n  full: {factors: {value: [ok, fail]}}\n  later: {factors: {value: [later]}}\n")
			writeFile(t, filepath.Join(root, "b.study.yaml"), overlappingStudy)
			err := Execute(context.Background(), testEnv(new(bytes.Buffer), new(bytes.Buffer)), Options{Project: root, Designs: []string{"a/full", "b/full", "a/later"}})
			if err == nil || !strings.Contains(err.Error(), "log:") {
				t.Fatalf("error=%v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "results", "b")); !os.IsNotExist(err) {
				t.Fatal("later study started")
			}
			logs, err := filepath.Glob(filepath.Join(root, "results", "a", "*", "*.txt"))
			if err != nil {
				t.Fatal(err)
			}
			want := 3
			if setupFailure {
				want = 1
			} else if got := lineCount(t, filepath.Join(root, "results", "a", "runs.jsonl")); got != 1 {
				t.Fatalf("completed runs=%d", got)
			}
			if len(logs) != want {
				t.Fatalf("logs=%v", logs)
			}
		})
	}
}

func TestExecuteEnvironmentResponsesAndLogs(t *testing.T) {
	for _, test := range []struct{ name, setup, run, wantError string }{
		{"nested", `printf 'out\n'; echo err >&2; echo '{"host":{"name":"test"}}'`, `echo log; echo err >&2; echo '{"summary":{"n":2},"samples":[1,null]}'`, ""},
		{"no env", "echo setup done", "echo '{}'", ""},
		{"reserved response", "", `echo '{"end":1}'`, `response name "end" is reserved`},
		{"factor response", "", `echo '{"value":1}'`, `response name "value" is also a factor`},
		{"no result", "", "true", "run produced no result"},
		{"bad result", "", "echo '[]'", "cannot unmarshal array"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "a.study.yaml"), fmt.Sprintf("setup: %q\nrun: %q\ndesigns:\n  full: {factors: {value: [x]}}\n", test.setup, test.run))
			stdout, stderr := newBuffers()
			err := Execute(context.Background(), testEnv(stdout, stderr), Options{Project: root, Designs: []string{"full"}})
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
			}
			output := filepath.Join(root, "results", "a")
			e := readRecords[model.Experiment](t, filepath.Join(output, "experiments.jsonl"))[0]
			r := readRecords[map[string]any](t, filepath.Join(output, "runs.jsonl"))[0]
			if test.name == "nested" {
				if e.Env["host"].(map[string]any)["name"] != "test" || r["summary"].(map[string]any)["n"] != float64(2) {
					t.Fatalf("env=%v run=%v", e.Env, r)
				}
				assertFileContains(t, filepath.Join(output, e.ID, "setup.txt"), "out\n", "err\n", `"host"`)
				assertFileContains(t, filepath.Join(output, e.ID, r["run_id"].(string)+".txt"), "log\n", "err\n", `"samples"`)
			} else if len(e.Env) != 0 {
				t.Fatalf("env=%v", e.Env)
			}
		})
	}
}

func TestExecuteCancellation(t *testing.T) {
	for _, concurrency := range []int{1, 2} {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "a.study.yaml"), fmt.Sprintf("run: echo interrupted; sleep 30; echo '{}'\ndesigns:\n  full:\n    factors: {value: [1, 2]}\n    concurrency: %d\n", concurrency))
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		err := Execute(ctx, testEnv(new(bytes.Buffer), new(bytes.Buffer)), Options{Project: root, Designs: []string{"full"}})
		cancel()
		if err == nil || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("error=%v context=%v", err, ctx.Err())
		}
		logs, err := filepath.Glob(filepath.Join(root, "results", "a", "*", "*.txt"))
		if err != nil || len(logs) != concurrency {
			t.Fatalf("logs=%v err=%v", logs, err)
		}
		for _, path := range logs {
			assertFileContains(t, path, "interrupted\n")
		}
	}
}

func TestCanonicalPath(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "project")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	got, err := canonicalPath(link)
	if err != nil || got != want {
		t.Fatalf("path=%q err=%v", got, err)
	}
}

func TestParseObjectAllowsNestedValues(t *testing.T) {
	object, err := parseObject(`{"object":{"n":1},"array":[true,null]}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := object["object"].(map[string]any)["n"].(json.Number); !ok {
		t.Fatal("number precision lost")
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
		t.Fatalf("last=%q", last)
	}
	for _, want := range []string{"stdout log\n", "stderr log\n", "{\"ok\":true}\n"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log %q missing %q", log.String(), want)
		}
	}
}

func TestLoadResultsIndexesRunsByDuration(t *testing.T) {
	output := t.TempDir()
	writeFile(t, filepath.Join(output, "experiments.jsonl"), `{"experiment_id":"experiment","study":"compression","designs":["smoke"],"factors":["value"]}`+"\n")
	writeFile(t, filepath.Join(output, "runs.jsonl"), `{"experiment_id":"experiment","replicate":1,"start":"2026-09-19T12:00:00Z","end":"2026-09-19T12:00:02.25Z","value":"one"}`+"\n")
	results, err := loadResults(output)
	if err != nil {
		t.Fatal(err)
	}
	key, err := reuseKey("compression", 1, model.Point{Values: []model.Value{{Name: "value", Value: "one"}}})
	if err != nil {
		t.Fatal(err)
	}
	if results.runs[key] != 2250*time.Millisecond {
		t.Fatalf("duration=%v", results.runs[key])
	}
}

func TestInterpolateDoesNotRescanValues(t *testing.T) {
	point := model.Point{Values: []model.Value{{Name: "a", Value: "{b}"}, {Name: "b", Value: "; echo injected"}}}
	got, err := interpolate("printf '%s\\n' {a} {b}", point)
	if err != nil {
		t.Fatal(err)
	}
	if want := `printf '%s\n' '{b}' '; echo injected'`; got != want {
		t.Fatalf("got=%q want=%q", got, want)
	}
}

func TestOutputSafety(t *testing.T) {
	for _, kind := range []string{"unowned", "marker", "symlink", "record symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "a.study.yaml"), overlappingStudy)
			output := filepath.Join(root, "results", "a")
			if err := os.MkdirAll(output, 0o755); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "unowned":
				writeFile(t, filepath.Join(output, "important"), "keep")
			case "marker":
				writeFile(t, filepath.Join(output, ".doe"), "wrong")
			case "symlink":
				if err := os.Remove(output); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), output); err != nil {
					t.Fatal(err)
				}
			case "record symlink":
				writeFile(t, filepath.Join(output, ".doe"), resultsMarker)
				if err := os.Symlink("missing", filepath.Join(output, "runs.jsonl")); err != nil {
					t.Fatal(err)
				}
			}
			if err := Execute(context.Background(), testEnv(new(bytes.Buffer), new(bytes.Buffer)), Options{Project: root, Designs: []string{"smoke"}}); err == nil {
				t.Fatal("unsafe output accepted")
			}
		})
	}
}

func testEnv(stdout, stderr *bytes.Buffer) *cli.Env {
	return &cli.Env{Stdin: strings.NewReader(""), Stdout: stdout, Stderr: stderr}
}

func newBuffers() (*bytes.Buffer, *bytes.Buffer) { return new(bytes.Buffer), new(bytes.Buffer) }

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

func readRecords[T any](t *testing.T, path string) []T {
	t.Helper()
	var records []T
	if err := readJSONL(path, func(data []byte) error {
		var record T
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		records = append(records, record)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return records
}

func assertFileContains(t *testing.T, path string, values ...string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if !bytes.Contains(data, []byte(value)) {
			t.Errorf("%s=%q missing %q", path, data, value)
		}
	}
}
