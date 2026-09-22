package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/felixge/doe/internal/cli"
)

func TestMainHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"-h"}, {"--help"}, {"help"}} {
		env, stdout, stderr := testEnv()
		if code := Main(context.Background(), env, args); code != 0 {
			t.Errorf("Main(%q) = %d, want 0", args, code)
		}
		if !strings.Contains(stdout.String(), "Usage: doe <command>") {
			t.Errorf("Main(%q) stdout = %q, want root usage", args, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("Main(%q) stderr = %q, want empty", args, stderr.String())
		}
	}
}

func TestMainUnknownCommand(t *testing.T) {
	env, _, stderr := testEnv()
	if code := Main(context.Background(), env, []string{"wat"}); code != 1 {
		t.Fatalf("Main() = %d, want 1", code)
	}
	if got := stderr.String(); got != "unknown command: wat\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestParseRunFlagsAnywhere(t *testing.T) {
	stderr := new(bytes.Buffer)
	opts, help, err := parseRun(stderr, []string{".", "first/smoke", "-d", "full", "--plan"})
	if err != nil || help {
		t.Fatalf("parseRun() = (%+v, %v, %v); stderr = %q", opts, help, err, stderr)
	}
	if !opts.Dirty || !opts.Plan || opts.Project != "." {
		t.Fatalf("options = %+v", opts)
	}
	if got := strings.Join(opts.Designs, ","); got != "first/smoke,full" {
		t.Fatalf("designs = %q", got)
	}
}

func TestRunHelp(t *testing.T) {
	env, stdout, stderr := testEnv()
	if code := Main(context.Background(), env, []string{"run", "--help"}); code != 0 {
		t.Fatalf("Main() = %d", code)
	}
	if !strings.Contains(stdout.String(), "Usage: doe run") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunArgumentErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "missing project", args: []string{"run"}, want: "a project directory is required"},
		{name: "unknown flag", args: []string{"run", "--wat", "."}, want: "unknown flag: --wat"},
		{name: "removed clean", args: []string{"run", "--clean", "."}, want: "unknown flag: --clean"},
		{name: "removed setup", args: []string{"run", "--setup", "."}, want: "unknown flag: --setup"},
	} {
		t.Run(test.name, func(t *testing.T) {
			env, _, stderr := testEnv()
			if code := Main(context.Background(), env, test.args); code != 1 {
				t.Fatalf("Main() = %d, want 1", code)
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.want)
			}
		})
	}
}

func TestRunPlanIntegration(t *testing.T) {
	root := writeStudy(t, "run: echo '{}'\ndesigns:\n  full:\n    factors: [{value: [one, two]}]\n")
	env, stdout, stderr := testEnv()
	if code := Main(context.Background(), env, []string{"run", root, "full", "--plan"}); code != 0 {
		t.Fatalf("Main() = %d; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "| point | value |") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunEmptyStdout(t *testing.T) {
	root := writeStudy(t, "setup: echo setup\nrun: echo '{}'\ndesigns:\n  full:\n    factors: [{value: [one]}]\n")
	env, stdout, stderr := testEnv()
	if code := Main(context.Background(), env, []string{"run", root, "full"}); code != 0 {
		t.Fatalf("Main() = %d; stderr = %q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunExecutionErrorIntegration(t *testing.T) {
	root := writeStudy(t, "run: exit 7\ndesigns:\n  full:\n    factors: [{value: [one]}]\n")
	env, _, stderr := testEnv()
	if code := Main(context.Background(), env, []string{"run", root, "full"}); code != 1 {
		t.Fatalf("Main() = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "exit status 7") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunCanceledContext(t *testing.T) {
	root := writeStudy(t, "run: echo '{}'\ndesigns:\n  full:\n    factors: [{value: [one]}]\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	env, _, stderr := testEnv()
	if code := Main(ctx, env, []string{"run", root, "full"}); code != 1 {
		t.Fatalf("Main() = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), context.Canceled.Error()) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func testEnv() (*cli.Env, *bytes.Buffer, *bytes.Buffer) {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	return &cli.Env{
		Stdin: strings.NewReader(""), Stdout: stdout, Stderr: stderr,
	}, stdout, stderr
}

func writeStudy(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "compression.study.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}
