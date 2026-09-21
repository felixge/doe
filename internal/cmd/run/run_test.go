package run

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/felixge/doe/internal/cli"
)

func TestCommandParsesOptions(t *testing.T) {
	env, _, stderr := commandTestEnv()
	var got Options
	execute := func(_ context.Context, _ *cli.Env, opts Options) error {
		got = opts
		return nil
	}
	args := []string{"a.yaml", "--plan", "b.yaml", "--force"}
	if code := Command(context.Background(), env, args, execute); code != 0 {
		t.Fatalf("Command() = %d; stderr = %q", code, stderr.String())
	}
	if !got.Plan || !got.Force {
		t.Fatalf("options = %+v", got)
	}
	if strings.Join(got.Designs, ",") != "a.yaml,b.yaml" {
		t.Fatalf("designs = %q", got.Designs)
	}
}

func TestCommandHelp(t *testing.T) {
	env, stdout, stderr := commandTestEnv()
	called := false
	if code := Command(context.Background(), env, []string{"--help"}, func(context.Context, *cli.Env, Options) error {
		called = true
		return nil
	}); code != 0 {
		t.Fatalf("Command() = %d", code)
	}
	if called {
		t.Fatal("execute called for help")
	}
	if !strings.Contains(stdout.String(), "Usage: doe run") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestCommandRequiresDesign(t *testing.T) {
	env, _, stderr := commandTestEnv()
	if code := Command(context.Background(), env, nil, func(context.Context, *cli.Env, Options) error {
		t.Fatal("execute called")
		return nil
	}); code != 1 {
		t.Fatalf("Command() = %d, want 1", code)
	}
	if got := stderr.String(); got != "error: at least one design is required\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestCommandRejectsUnknownFlag(t *testing.T) {
	env, _, stderr := commandTestEnv()
	if code := Command(context.Background(), env, []string{"--wat", "design.yaml"}, nil); code != 1 {
		t.Fatalf("Command() = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unknown flag: --wat") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestCommandReturnsExecutionError(t *testing.T) {
	env, _, stderr := commandTestEnv()
	want := errors.New("boom")
	if code := Command(context.Background(), env, []string{"design.yaml"}, func(context.Context, *cli.Env, Options) error {
		return want
	}); code != 1 {
		t.Fatalf("Command() = %d, want 1", code)
	}
	if got := stderr.String(); got != "error: boom\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func commandTestEnv() (*cli.Env, *bytes.Buffer, *bytes.Buffer) {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	return &cli.Env{
		Stdin:  strings.NewReader(""),
		Stdout: stdout,
		Stderr: stderr,
		NotifyContext: func(ctx context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
			return ctx, func() {}
		},
	}, stdout, stderr
}
