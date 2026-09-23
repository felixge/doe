package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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
		{"file", []string{"run", "-f", path}, [][3]int{{1, 4, 5}, {2, 4, 6}, {3, 4, 7}, {1, 5, 6}, {2, 5, 7}, {3, 5, 8}}},
		{"override", []string{"run", "-f", path, "foo=9"}, [][3]int{{9, 4, 13}, {9, 5, 14}}},
		{"flags and factors interspersed", []string{"run", "foo=[1, 2, 3]", "bar=[4, 5]", "--run", `printf '{"sum":%s}\n' "$((foo + bar))"`}, [][3]int{{1, 4, 5}, {2, 4, 6}, {3, 4, 7}, {1, 5, 6}, {2, 5, 7}, {3, 5, 8}}},
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
					Foo string `json:"foo"`
					Bar string `json:"bar"`
					Sum int    `json:"sum"`
				}
				if err := json.Unmarshal([]byte(line), &got); err != nil {
					t.Fatal(err)
				}
				want := tc.want[i]
				if got.Foo != stringInt(want[0]) || got.Bar != stringInt(want[1]) || got.Sum != want[2] {
					t.Errorf("result %d = %+v, want %v", i, got, want)
				}
			}
		})
	}
}

func stringInt(n int) string { return strconv.Itoa(n) }

func TestRunErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"run", "foo=1"}, "run script is required"},
		{[]string{"run", "foo=1", "--run", "exit 7"}, "exit status 7"},
		{[]string{"run", "foo=1", "--run", "echo nope"}, "JSON object"},
		{[]string{"results"}, "unknown command"},
	} {
		var stdout, stderr bytes.Buffer
		code := Main(context.Background(), &cli.Env{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}, tc.args)
		if code != 1 || !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("Main(%q) = %d, stderr %q; want %q", tc.args, code, &stderr, tc.want)
		}
	}
}

func TestNoResultsDirectory(t *testing.T) {
	// The current implementation only prints results and does not persist them.
	dir := t.TempDir()
	path := filepath.Join(dir, "sum.study.yaml")
	data, err := os.ReadFile(filepath.Join("..", "..", "example", "sum", "sum.study.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Main(context.Background(), &cli.Env{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}, []string{"run", "-f", path}); code != 0 {
		t.Fatalf("exit %d: %s", code, &stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "results")); !os.IsNotExist(err) {
		t.Fatalf("results directory should not exist: %v", err)
	}
}
