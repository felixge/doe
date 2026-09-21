package runner

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestProgressBarRendersSnapshots(t *testing.T) {
	var output bytes.Buffer
	bar := newProgress(&output, "design.yaml")
	bar.Render(progressSnapshot{total: 108, status: "estimating ..."})
	if got := output.String(); !strings.Contains(got, "design.yaml [>                       ] 0/108 · estimating ...") {
		t.Fatalf("estimating progress output = %q", got)
	}

	bar.Render(progressSnapshot{total: 108, done: 12, status: "~3m 42s remaining"})
	if got := output.String(); !strings.Contains(got, "design.yaml [==>                     ] 12/108 · ~3m 42s remaining") {
		t.Fatalf("estimated progress output = %q", got)
	}

	bar.Render(progressSnapshot{total: 108, done: 108, status: "done in 4m 11s"})
	bar.Close()
	if got := output.String(); !strings.Contains(got, "design.yaml [========================] 108/108 · done in 4m 11s\n") {
		t.Fatalf("completed progress output = %q", got)
	}
}

func TestProgressBarClearsEachRedraw(t *testing.T) {
	var output bytes.Buffer
	bar := newProgress(&output, "design.yaml")
	bar.Render(progressSnapshot{total: 2, status: "estimating ..."})
	bar.Render(progressSnapshot{total: 2, done: 1, status: "~1s remaining"})
	if got := strings.Count(output.String(), "\r\x1b[2K"); got != 2 {
		t.Fatalf("line clear count = %d, want 2; output = %q", got, output.String())
	}
	bar.Clear()
	bar.Clear()
	if got := strings.Count(output.String(), "\r\x1b[2K"); got != 3 {
		t.Fatalf("line clear count after Clear = %d, want 3; output = %q", got, output.String())
	}
}

func TestProgressStateUsesGlobalAverageForUnseenPoints(t *testing.T) {
	started := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	state := newProgressState(3, 1, started, []string{"new-a", "new-b"})
	state.AddDuration("old", 4*time.Second)
	state.Start(started)

	if got, ok := state.estimate(started.Add(time.Second)); !ok || got != 7*time.Second {
		t.Fatalf("estimate = %s, %v; want 7s, true", got, ok)
	}
}

func TestProgressStatePrefersPointAverages(t *testing.T) {
	started := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	state := newProgressState(5, 2, started, []string{"fast", "slow", "fast"})
	state.AddDuration("fast", 2*time.Second)
	state.AddDuration("slow", 10*time.Second)

	if got, ok := state.estimate(started); !ok || got != 14*time.Second {
		t.Fatalf("estimate = %s, %v; want 14s, true", got, ok)
	}
}

func TestProgressStateWaitsForFirstDuration(t *testing.T) {
	started := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	state := newProgressState(2, 0, started, []string{"a", "b"})
	state.Start(started)
	now := started.Add(3 * time.Second)

	if got, ok := state.estimate(now); ok || got != 0 {
		t.Fatalf("estimate = %s, %v; want 0s, false", got, ok)
	}
	if got := state.Snapshot(now).status; got != "estimating ... · 3s elapsed" {
		t.Fatalf("status = %q", got)
	}
}

func TestProgressStateCompletionUpdatesPointAverage(t *testing.T) {
	started := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	state := newProgressState(3, 1, started, []string{"point", "point"})
	state.AddDuration("point", 4*time.Second)
	state.Start(started)
	state.Complete(6 * time.Second)

	if got, ok := state.estimate(started.Add(6 * time.Second)); !ok || got != 5*time.Second {
		t.Fatalf("estimate = %s, %v; want 5s, true", got, ok)
	}
	state.Start(started.Add(6 * time.Second))
	state.Complete(5 * time.Second)
	if got := state.Snapshot(started.Add(11 * time.Second)).status; got != "done in 11s" {
		t.Fatalf("status = %q", got)
	}
}

func TestFormatDuration(t *testing.T) {
	for duration, want := range map[time.Duration]string{
		0:                              "0s",
		1500 * time.Millisecond:        "2s",
		3*time.Minute + 42*time.Second: "3m 42s",
		time.Hour + 2*time.Minute + 3*time.Second: "1h 2m 3s",
	} {
		if got := formatDuration(duration); got != want {
			t.Errorf("formatDuration(%s) = %q, want %q", duration, got, want)
		}
	}
}
