package runner

import (
	"fmt"
	"io"
	"strings"
	"time"
)

const progressWidth = 24

type progressSnapshot struct {
	total  int
	done   int
	status string
}

type progressBar struct {
	writer io.Writer
	label  string
	shown  bool
}

func newProgress(writer io.Writer, label string) *progressBar {
	return &progressBar{writer: writer, label: label}
}

func (p *progressBar) Render(snapshot progressSnapshot) {
	bar := strings.Repeat("=", progressWidth)
	if snapshot.done < snapshot.total {
		filled := 0
		if snapshot.total > 0 {
			filled = snapshot.done * progressWidth / snapshot.total
		}
		bar = strings.Repeat("=", filled) + ">" + strings.Repeat(" ", progressWidth-filled-1)
	}
	_, _ = fmt.Fprintf(p.writer, "\r\x1b[2K[%s] %s %d/%d · %s", bar, p.label, snapshot.done, snapshot.total, snapshot.status)
	p.shown = true
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

type durationStats struct {
	count int
	total time.Duration
}

func (s *durationStats) Add(duration time.Duration) {
	s.count++
	s.total += duration
}

func (s durationStats) Average() (time.Duration, bool) {
	if s.count == 0 {
		return 0, false
	}
	return s.total / time.Duration(s.count), true
}

type progressState struct {
	total         int
	done          int
	started       time.Time
	activeStarted time.Time
	remaining     []string
	next          int
	all           durationStats
	points        map[string]durationStats
}

func newProgressState(total, done int, started time.Time, remaining []string) *progressState {
	return &progressState{
		total: total, done: done, started: started, remaining: remaining,
		points: make(map[string]durationStats),
	}
}

func (p *progressState) AddDuration(point string, duration time.Duration) {
	p.all.Add(duration)
	stats := p.points[point]
	stats.Add(duration)
	p.points[point] = stats
}

func (p *progressState) duration(point string) (time.Duration, bool) {
	if duration, ok := p.points[point].Average(); ok {
		return duration, true
	}
	return p.all.Average()
}

func (p *progressState) Start(now time.Time) {
	p.activeStarted = now
}

func (p *progressState) Complete(duration time.Duration) {
	p.AddDuration(p.remaining[p.next], duration)
	p.next++
	p.done++
	p.activeStarted = time.Time{}
}

func (p *progressState) Snapshot(now time.Time) progressSnapshot {
	snapshot := progressSnapshot{total: p.total, done: p.done, status: "estimating ..."}
	if p.done == p.total {
		snapshot.status = "done in " + formatDuration(now.Sub(p.started))
		return snapshot
	}
	if estimate, ok := p.estimate(now); ok {
		snapshot.status = "~" + formatDuration(estimate) + " remaining"
	} else if !p.activeStarted.IsZero() {
		snapshot.status += " · " + formatDuration(now.Sub(p.activeStarted)) + " elapsed"
	}
	return snapshot
}

func (p *progressState) estimate(now time.Time) (time.Duration, bool) {
	var remaining time.Duration
	for index, point := range p.remaining[p.next:] {
		estimate, ok := p.duration(point)
		if !ok {
			return 0, false
		}
		if index == 0 && !p.activeStarted.IsZero() {
			estimate -= now.Sub(p.activeStarted)
		}
		remaining += max(estimate, 0)
	}
	return remaining, true
}

func (p *progressState) estimateConcurrent(
	now time.Time,
	pointKeys []string,
	groupQueues map[string][]runTask,
	next map[string]int,
	active map[runTask]time.Time,
	concurrency int,
) (time.Duration, bool) {
	var remaining time.Duration
	for group, queue := range groupQueues {
		lanes := make([]time.Duration, 0, concurrency)
		for _, task := range queue[:next[group]] {
			started, ok := active[task]
			if !ok {
				continue
			}
			duration, ok := p.duration(pointKeys[task.pointIndex])
			if !ok {
				return 0, false
			}
			lanes = append(lanes, max(duration-now.Sub(started), 0))
		}
		for len(lanes) < concurrency {
			lanes = append(lanes, 0)
		}
		for _, task := range queue[next[group]:] {
			duration, ok := p.duration(pointKeys[task.pointIndex])
			if !ok {
				return 0, false
			}
			lane := 0
			for i := 1; i < len(lanes); i++ {
				if lanes[i] < lanes[lane] {
					lane = i
				}
			}
			lanes[lane] += duration
		}
		for _, duration := range lanes {
			remaining = max(remaining, duration)
		}
	}
	return remaining, true
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
