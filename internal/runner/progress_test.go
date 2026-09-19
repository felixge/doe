package runner

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestProgressBar(t *testing.T) {
	var redirected bytes.Buffer
	newProgress(&redirected, "design.yaml", 4, 0, 0).Complete(time.Second)
	if redirected.Len() != 0 {
		t.Fatalf("redirected progress output = %q", redirected.String())
	}

	var output bytes.Buffer
	started := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	now := started
	bar := &progressBar{
		output: &output, writer: &output, label: "design.yaml", total: 108,
		started: started, now: func() time.Time { return now },
	}
	bar.render()
	if got := output.String(); !strings.Contains(got, "design.yaml [>                       ] 0/108 · estimating ...") {
		t.Fatalf("estimating progress output = %q", got)
	}

	bar.done = 12
	wantBar := "[==>                     ]"
	bar.doneDuration = 27750 * time.Millisecond
	bar.render()
	if got := output.String(); !strings.Contains(got, "design.yaml "+wantBar+" 12/108 · ~3m 42s remaining") {
		t.Fatalf("estimated progress output = %q", got)
	}

	bar.done = 108
	now = started.Add(4*time.Minute + 11*time.Second)
	bar.render()
	bar.Close()
	if got := output.String(); !strings.Contains(got, "design.yaml [========================] 108/108 · done in 4m 11s\n") {
		t.Fatalf("completed progress output = %q", got)
	}
}

func TestProgressBarClearsEachRedraw(t *testing.T) {
	var output bytes.Buffer
	bar := &progressBar{
		output: &output, writer: &output, label: "design.yaml", total: 2,
		doneDuration: time.Minute,
		started:      time.Now(), now: time.Now,
	}
	bar.render()
	bar.Complete(time.Second)
	if got := strings.Count(output.String(), "\r\x1b[2K"); got != 2 {
		t.Fatalf("line clear count = %d, want 2; output = %q", got, output.String())
	}
}

func TestProgressEstimateCombinesHistoricalAndCurrentDurations(t *testing.T) {
	bar := &progressBar{
		writer:       io.Discard,
		total:        3,
		done:         1,
		doneDuration: 4 * time.Second,
	}
	bar.Complete(8 * time.Second)
	if got, ok := bar.estimate(); !ok || got != 6*time.Second {
		t.Fatalf("estimate = %s, %v; want 6s, true", got, ok)
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
