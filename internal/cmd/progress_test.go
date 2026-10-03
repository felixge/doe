package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/felixge/doe/internal/cli"
	"github.com/felixge/doe/internal/model"
	"github.com/felixge/doe/internal/results"
)

// Each write is a complete ID line or status frame, observed without racing stdout.
type frameWriter chan string

func (w frameWriter) Write(p []byte) (int, error) {
	w <- string(p)
	return len(p), nil
}

func nextFrame(t *testing.T, frames frameWriter) string {
	t.Helper()
	select {
	case frame := <-frames:
		return frame
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for output")
		return ""
	}
}

func TestExperimentPrintsIDBeforeSetupFinishes(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := t.Context()
	frames := make(frameWriter, 10)
	done := make(chan int, 1)
	go func() {
		done <- Main(ctx, &cli.Env{Stdout: frames, Stderr: io.Discard}, []string{
			"experiment", "foo=1", "-s", "while [ ! -f gate ]; do sleep 0.01; done",
			"-r", "echo '{}'; true '{foo}'",
		})
	}()
	idLine := nextFrame(t, frames)
	id, err := uuid.Parse(strings.TrimSuffix(idLine, "\n"))
	if err != nil || idLine != id.String()+"\n" {
		t.Fatalf("ID output = %q, %v", idLine, err)
	}
	experiment, err := results.Open("results").ReadExperiment(id)
	if err != nil || experiment.State != model.StateSetup {
		t.Fatalf("experiment at ID output = %+v, %v", experiment, err)
	}
	select {
	case code := <-done:
		t.Fatalf("experiment finished before setup gate opened: %d", code)
	default:
	}
	if err := os.WriteFile("gate", nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code = %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("experiment did not finish")
	}
	if len(frames) != 0 {
		t.Errorf("unexpected additional output: %q", <-frames)
	}
}

func TestWatchExperiment(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state model.State
		err   error
	}{
		{"done", model.StateDone, nil},
		{"error", model.StateError, errors.New("setup failed")},
		{"canceled", model.StateStopped, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resultFiles, err := results.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			experiment := model.NewExperiment(model.Design{Factors: model.Factors{"foo": {1}}, Replicates: 1})
			release, err := resultFiles.Lock(experiment.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = release() }()
			log, err := resultFiles.CreateExperiment(&experiment)
			if err != nil {
				t.Fatal(err)
			}
			if err := log.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			frames := make(frameWriter, 10)
			done := make(chan error, 1)
			finish := make(chan struct{})
			start := make(chan struct{})
			go func() {
				run := func() error {
					experiment.State = model.StateRunning
					if err := resultFiles.UpdateExperiment(&experiment); err != nil {
						return err
					}
					select {
					case <-finish:
					case <-ctx.Done():
					}
					experiment.State = tc.state
					experiment.End = time.Now()
					if tc.state == model.StateError {
						experiment.Error = tc.err.Error()
					}
					return errors.Join(tc.err, resultFiles.UpdateExperiment(&experiment))
				}
				workerDone := make(chan error, 1)
				go func() {
					<-start
					workerDone <- run()
				}()
				done <- watchExperiment(frames, resultFiles, workerDone, cancel)
			}()
			initial := nextFrame(t, frames)
			if !strings.Contains(initial, "State: Setup\n") || strings.Contains(initial, "\x1b") {
				t.Fatalf("initial frame = %q", initial)
			}
			close(start)
			running := nextFrame(t, frames)
			if !strings.HasPrefix(running, "\x1b[5A\r\x1b[J") || !strings.Contains(running, "State: Running\n") || strings.Contains(running, "Setup log:") {
				t.Fatalf("refresh = %q", running)
			}
			if tc.state == model.StateStopped {
				cancel()
			} else {
				close(finish)
			}
			select {
			case err := <-done:
				if !errors.Is(err, tc.err) {
					t.Errorf("watch error = %v, want %v", err, tc.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("watch did not finish")
			}
			final := nextFrame(t, frames)
			status, err := resultFiles.Status()
			if err != nil {
				t.Fatal(err)
			}
			want := "\x1b[4A\r\x1b[J" + formatStatus(status, resultFiles)
			if final != want || len(frames) != 0 {
				t.Errorf("final frame = %q, want %q; extra frames = %d", final, want, len(frames))
			}
		})
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestExperimentIgnoresStdoutFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	var stderr strings.Builder
	code := Main(t.Context(), &cli.Env{Stdout: failingWriter{}, Stderr: &stderr},
		[]string{"experiment", "foo=1", "-r", "echo '{}'; true '{foo}'"})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	status, err := results.Open("results").Status()
	if err != nil || status == nil || status.State != results.StateDone {
		t.Errorf("status = %+v, %v; want Done", status, err)
	}
}

func TestStatusLines(t *testing.T) {
	for _, tc := range []struct {
		output string
		width  int
		want   int
	}{
		{"abc\ndef\n", 0, 2},
		{"abc\ndef\n", 3, 2},
		{"abcd\n\n", 3, 3},
		{"abcdefg\n", 3, 3},
		{"日本語\n", 4, 2},
	} {
		if got := statusLines(tc.output, tc.width); got != tc.want {
			t.Errorf("statusLines(%q, %d) = %d, want %d", tc.output, tc.width, got, tc.want)
		}
	}
}

func TestIsTerminal(t *testing.T) {
	if isTerminal(io.Discard) {
		t.Error("io.Discard is a terminal")
	}
	file, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if isTerminal(file) {
		t.Error("regular file is a terminal")
	}
}
