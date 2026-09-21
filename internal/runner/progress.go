package runner

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

const progressWidth = 24

type progressBar struct {
	mu           sync.Mutex
	output       io.Writer
	writer       io.Writer
	label        string
	total        int
	done         int
	doneDuration time.Duration
	started      time.Time
	runStarted   time.Time
	now          func() time.Time
	shown        bool
	stop         chan struct{}
	stopped      chan struct{}
}

func newProgress(writer io.Writer, label string, total, done int, doneDuration time.Duration) *progressBar {
	bar := &progressBar{
		output:       writer,
		writer:       io.Discard,
		label:        label,
		total:        total,
		done:         done,
		doneDuration: doneDuration,
		started:      time.Now(),
		now:          time.Now,
	}
	if isTerminal(writer) {
		bar.writer = writer
	}
	bar.render()
	if bar.writer != io.Discard {
		bar.stop = make(chan struct{})
		bar.stopped = make(chan struct{})
		go bar.refresh()
	}
	return bar
}

func isTerminal(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func (p *progressBar) StartRun() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.runStarted = p.now()
	p.renderLocked()
}

func (p *progressBar) Complete(duration time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done++
	p.doneDuration += duration
	p.runStarted = time.Time{}
	p.renderLocked()
}

func (p *progressBar) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearLocked()
	return p.output.Write(data)
}

func (p *progressBar) clearLocked() {
	if p.shown {
		_, _ = fmt.Fprint(p.writer, "\r\x1b[2K")
		p.shown = false
	}
}

func (p *progressBar) Close() {
	if p.stop != nil {
		close(p.stop)
		<-p.stopped
		p.stop = nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.shown {
		_, _ = fmt.Fprintln(p.writer)
		p.shown = false
	}
}

func (p *progressBar) render() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.renderLocked()
}

func (p *progressBar) renderLocked() {
	bar := strings.Repeat("=", progressWidth)
	if p.done < p.total {
		filled := 0
		if p.total > 0 {
			filled = p.done * progressWidth / p.total
		}
		bar = strings.Repeat("=", filled) + ">" + strings.Repeat(" ", progressWidth-filled-1)
	}
	status := "estimating ..."
	if p.done == p.total {
		status = "done in " + formatDuration(p.now().Sub(p.started))
	} else if estimate, ok := p.estimate(); ok {
		status = "~" + formatDuration(estimate) + " remaining"
	} else if !p.runStarted.IsZero() {
		status = "estimating ... · " + formatDuration(p.now().Sub(p.runStarted)) + " elapsed"
	}
	_, _ = fmt.Fprintf(p.writer, "\r\x1b[2K%s [%s] %d/%d · %s", p.label, bar, p.done, p.total, status)
	p.shown = true
}

func (p *progressBar) estimate() (time.Duration, bool) {
	if p.done == 0 {
		return 0, false
	}
	average := p.doneDuration / time.Duration(p.done)
	remaining := time.Duration(p.total-p.done) * average
	if !p.runStarted.IsZero() {
		remaining -= p.now().Sub(p.runStarted)
	}
	if remaining < 0 {
		remaining = 0
	}
	return remaining, true
}

func (p *progressBar) refresh() {
	ticker := time.NewTicker(time.Second)
	defer func() {
		ticker.Stop()
		close(p.stopped)
	}()
	for {
		select {
		case <-ticker.C:
			p.render()
		case <-p.stop:
			return
		}
	}
}

func formatDuration(duration time.Duration) string {
	duration = max(duration.Round(time.Second), 0)
	hours := duration / time.Hour
	duration %= time.Hour
	minutes := duration / time.Minute
	seconds := duration % time.Minute / time.Second
	parts := make([]string, 0, 3)
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if seconds > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}
	return strings.Join(parts, " ")
}
