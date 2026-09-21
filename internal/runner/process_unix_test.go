package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCommandOutputCancellationKillsDescendants(t *testing.T) {
	pidFile := t.TempDir() + "/child.pid"
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := commandOutput(ctx, testEnv(new(bytes.Buffer), new(bytes.Buffer)), t.TempDir(), "sleep 1000 & echo $! > "+shellQuote(pidFile)+"; wait")
		result <- err
	}()

	pid := waitForPID(t, pidFile)
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("commandOutput() succeeded after cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("commandOutput() did not return after cancellation")
	}
	waitForProcessExit(t, pid)
}

func TestCommandOutputScannerErrorKillsDescendants(t *testing.T) {
	pidFile := t.TempDir() + "/child.pid"
	script := "sleep 1000 & echo $! > " + shellQuote(pidFile) + "; head -c 16777217 /dev/zero | tr '\\000' x; wait"
	result := make(chan error, 1)
	go func() {
		_, err := commandOutput(context.Background(), testEnv(new(bytes.Buffer), new(bytes.Buffer)), t.TempDir(), script)
		result <- err
	}()

	pid := waitForPID(t, pidFile)
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "token too long") {
			t.Fatalf("commandOutput() error = %v, want scanner token-too-long error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("commandOutput() did not return after scanner error")
	}
	waitForProcessExit(t, pid)
}

func waitForPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatalf("parse child PID: %v", err)
			}
			return pid
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read child PID: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("child PID was not written")
	return 0
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatalf("check child process: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("child process %d is still running", pid)
}
