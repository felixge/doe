package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/felixge/doe2/internal/cli"
)

func TestSumIntegration(t *testing.T) {
	path := filepath.Join("..", "..", "example", "sum", "sum.study.yaml")
	for _, tc := range []struct {
		name string
		args []string
		want [][3]int
	}{
		{"file", []string{"execute", "-f", path}, [][3]int{{1, 4, 5}, {2, 4, 6}, {3, 4, 7}, {1, 5, 6}, {2, 5, 7}, {3, 5, 8}}},
		{"override", []string{"execute", "-f", path, "foo=9"}, [][3]int{{9, 4, 13}, {9, 5, 14}}},
		{"flags and factors interspersed", []string{"execute", "foo=[1, 2, 3]", "bar=[4, 5]", "-r", `printf '{"sum":%s}\n' "$((foo + bar))"`}, [][3]int{{1, 4, 5}, {2, 4, 6}, {3, 4, 7}, {1, 5, 6}, {2, 5, 7}, {3, 5, 8}}},
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
					Foo int `json:"foo"`
					Bar int `json:"bar"`
					Sum int `json:"sum"`
				}
				if err := json.Unmarshal([]byte(line), &got); err != nil {
					t.Fatal(err)
				}
				want := tc.want[i]
				if got.Foo != want[0] || got.Bar != want[1] || got.Sum != want[2] {
					t.Errorf("result %d = %+v, want %v", i, got, want)
				}
			}
		})
	}
}

func TestStructuredSettings(t *testing.T) {
	var stdout, stderr bytes.Buffer
	env := &cli.Env{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}
	args := []string{"execute", "foo=[{bar: 1}]", "enabled=true", "label=hello", "-r", `printf '{"seen":%s,"active":%s,"text":"%s"}\n' "$foo" "$enabled" "$label"`}
	if code := Main(context.Background(), env, args); code != 0 {
		t.Fatalf("exit code %d: %s", code, &stderr)
	}
	var got struct {
		Foo     map[string]int `json:"foo"`
		Enabled bool           `json:"enabled"`
		Label   string         `json:"label"`
		Seen    map[string]int `json:"seen"`
		Active  bool           `json:"active"`
		Text    string         `json:"text"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Foo["bar"] != 1 || got.Seen["bar"] != 1 || !got.Enabled || !got.Active || got.Label != "hello" || got.Text != "hello" {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestExecuteHelp(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "execute  Execute a study"},
		{[]string{"execute", "--help"}, "Usage: doe execute"},
	} {
		var stdout, stderr bytes.Buffer
		code := Main(context.Background(), &cli.Env{Stdout: &stdout, Stderr: &stderr}, tc.args)
		if code != 0 || !strings.Contains(stdout.String(), tc.want) || stderr.Len() != 0 {
			t.Errorf("Main(%q) = %d, stdout %q, stderr %q; want %q", tc.args, code, &stdout, &stderr, tc.want)
		}
	}
}

func TestExecuteErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"execute", "foo=1"}, "run script is required"},
		{[]string{"execute", "foo=1", "--run", "exit 7"}, "exit status 7"},
		{[]string{"execute", "foo=1", "--run", "echo nope"}, "JSON object"},
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
