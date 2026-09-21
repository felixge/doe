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
	opts, help, err := parseRun(stderr, []string{"first.yaml", "-d", "second.yaml", "--plan", "-c", "-s"})
	if err != nil || help {
		t.Fatalf("parseRun() = (%+v, %v, %v); stderr = %q", opts, help, err, stderr)
	}
	if !opts.Dirty || !opts.Plan || !opts.Clean || !opts.Setup {
		t.Fatalf("options = %+v", opts)
	}
	if got := strings.Join(opts.Designs, ","); got != "first.yaml,second.yaml" {
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
		{name: "missing design", args: []string{"run"}, want: "at least one design is required"},
		{name: "unknown flag", args: []string{"run", "--wat", "design.yaml"}, want: "unknown flag: --wat"},
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
	design := writeDesign(t, "run: echo '{}'\nfactors:\n  - value: [one, two]\n")
	env, stdout, stderr := testEnv()
	if code := Main(context.Background(), env, []string{"run", design, "--plan"}); code != 0 {
		t.Fatalf("Main() = %d; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "| point | value |") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunSetupIntegration(t *testing.T) {
	root := t.TempDir()
	design := filepath.Join(root, "design.yaml")
	if err := os.WriteFile(design, []byte("setup: echo setup > setup.txt\nfactors: [{value: [one]}]\nrun: echo run > run.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"results", "work"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env, stdout, stderr := testEnv()
	if code := Main(context.Background(), env, []string{"run", design, "--clean", "--setup"}); code != 0 {
		t.Fatalf("Main() = %d; stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "setup.txt")); err != nil {
		t.Fatalf("setup was not run: %v", err)
	}
	for _, name := range []string{"run.txt", "results", "work"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("%s exists after --setup: %v", name, err)
		}
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunExecutionErrorIntegration(t *testing.T) {
	design := writeDesign(t, "run: exit 7\nfactors:\n  - value: [one]\n")
	env, _, stderr := testEnv()
	if code := Main(context.Background(), env, []string{"run", design}); code != 1 {
		t.Fatalf("Main() = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "exit status 7") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunCanceledContext(t *testing.T) {
	design := writeDesign(t, "run: echo '{}'\nfactors:\n  - value: [one]\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	env, _, stderr := testEnv()
	if code := Main(ctx, env, []string{"run", design}); code != 1 {
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

func writeDesign(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "design.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
