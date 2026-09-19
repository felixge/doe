package runner

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

const progressWidth = 24

type progressBar struct {
	output       io.Writer
	writer       io.Writer
	label        string
	total        int
	done         int
	doneDuration time.Duration
	started      time.Time
	now          func() time.Time
	shown        bool
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
	return bar
}

func isTerminal(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func (p *progressBar) Complete(duration time.Duration) {
	p.done++
	p.doneDuration += duration
	p.render()
}

func (p *progressBar) LogWriter() io.Writer {
	return &progressLogWriter{progress: p, writer: p.output}
}

func (p *progressBar) Clear() {
	if p.shown {
		_, _ = fmt.Fprint(p.writer, "\r\x1b[2K")
		p.shown = false
	}
}

func (p *progressBar) Close() {
	if p.shown {
		_, _ = fmt.Fprintln(p.writer)
		p.shown = false
	}
}

func (p *progressBar) render() {
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
	}
	_, _ = fmt.Fprintf(p.writer, "\r\x1b[2K%s [%s] %d/%d · %s", p.label, bar, p.done, p.total, status)
	p.shown = true
}

func (p *progressBar) estimate() (time.Duration, bool) {
	if p.done == 0 {
		return 0, false
	}
	average := p.doneDuration / time.Duration(p.done)
	return time.Duration(p.total-p.done) * average, true
}

func formatDuration(duration time.Duration) string {
	duration = duration.Round(time.Second)
	if duration < 0 {
		duration = 0
	}
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

type progressLogWriter struct {
	progress *progressBar
	writer   io.Writer
}

func (w *progressLogWriter) Write(data []byte) (int, error) {
	w.progress.Clear()
	return w.writer.Write(data)
}
