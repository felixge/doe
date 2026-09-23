package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/felixge/doe2/internal/cli"
	"github.com/felixge/doe2/internal/model"
)

func TestExperimentIntegration(t *testing.T) {
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
	for _, tc := range []struct {
		name string
		args []string
		want [][2]int
	}{
		{"file", []string{"experiment", "-f", path}, [][2]int{{1, 4}, {2, 4}, {3, 4}, {1, 5}, {2, 5}, {3, 5}}},
		{"override", []string{"experiment", "-f", path, "foo=9"}, [][2]int{{9, 4}, {9, 5}}},
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
			lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
			if len(lines) != len(tc.want) {
				t.Fatalf("got %d results, want %d: %s", len(lines), len(tc.want), &stdout)
			}
			for i, line := range lines {
				var got struct {
					Foo    int `json:"foo"`
					Bar    int `json:"bar"`
					Result int `json:"result"`
				}
				if err := json.Unmarshal([]byte(line), &got); err != nil {
					t.Fatal(err)
				}
				want := tc.want[i]
				if got.Foo != want[0] || got.Bar != want[1] || got.Result != want[0]+want[1] {
					t.Errorf("result %d = %+v, want %v", i, got, want)
				}
			}
		})
	}
}

func TestExpandRunScript(t *testing.T) {
	values := map[model.Factor]model.Setting{"foo": "a'b; {bar}", "bar": 42}
	got := expandRunScript(`{foo} {bar} {missing} {"result":1}`, values)
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
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"experiment", "foo=1"}, "run script is required"},
		{[]string{"experiment", "foo=1", "--run", "exit 7"}, "exit status 7"},
		{[]string{"experiment", "foo=1", "--run", "echo nope"}, "JSON object"},
		{[]string{"execute"}, "unknown command"},
		{[]string{"run"}, "unknown command"},
		{[]string{"results"}, "unknown command"},
	} {
		var stdout, stderr bytes.Buffer
		code := Main(context.Background(), &cli.Env{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}, tc.args)
		if code != 1 || !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("Main(%q) = %d, stderr %q; want %q", tc.args, code, &stderr, tc.want)
		}
	}
}
