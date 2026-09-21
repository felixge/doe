package cmd

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/felixge/doe/internal/cli"
	runcmd "github.com/felixge/doe/internal/cmd/run"
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

func TestMainDoesNotParseRunFlags(t *testing.T) {
	env, _, stderr := testEnv()
	var got runcmd.Options
	execute := func(_ context.Context, _ *cli.Env, opts runcmd.Options) error {
		got = opts
		return nil
	}
	args := []string{"run", "first.yaml", "-d", "second.yaml", "-p"}
	if code := MainWithRun(context.Background(), env, args, execute); code != 0 {
		t.Fatalf("MainWithRun() = %d; stderr = %q", code, stderr.String())
	}
	if !got.Dirty || !got.Plan {
		t.Fatalf("options = %+v", got)
	}
	if strings.Join(got.Designs, ",") != "first.yaml,second.yaml" {
		t.Fatalf("designs = %q", got.Designs)
	}
}

func TestMainUsesSignalContext(t *testing.T) {
	env, _, stderr := testEnv()
	type contextKey struct{}
	stopCalled := false
	env.NotifyContext = func(ctx context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
		if len(signals) != 2 {
			t.Fatalf("signals = %v, want interrupt and SIGTERM", signals)
		}
		return context.WithValue(ctx, contextKey{}, true), func() { stopCalled = true }
	}
	execute := func(ctx context.Context, _ *cli.Env, _ runcmd.Options) error {
		if value, _ := ctx.Value(contextKey{}).(bool); !value {
			t.Fatal("execute did not receive signal context")
		}
		return nil
	}
	if code := MainWithRun(context.Background(), env, []string{"run", "design.yaml"}, execute); code != 0 {
		t.Fatalf("MainWithRun() = %d; stderr = %q", code, stderr.String())
	}
	if !stopCalled {
		t.Fatal("signal context was not stopped")
	}
}

func testEnv() (*cli.Env, *bytes.Buffer, *bytes.Buffer) {
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
