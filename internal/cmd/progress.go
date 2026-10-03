package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/felixge/doe/internal/results"
	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

func isTerminal(w io.Writer) bool {
	file, ok := w.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(file.Fd()))
}

// statusLines counts screen rows, including lines wrapped by the terminal.
func statusLines(output string, width int) int {
	lines := strings.Count(output, "\n")
	if width > 0 {
		for line := range strings.SplitSeq(strings.TrimSuffix(output, "\n"), "\n") {
			lines += max(runewidth.StringWidth(line)-1, 0) / width
		}
	}
	return lines
}

// watchExperiment owns stdout; the worker only writes experiment records and logs.
func watchExperiment(w io.Writer, resultFiles *results.Results, done <-chan error, cancel context.CancelFunc) error {
	var lines int
	redraw := func() error {
		status, err := resultFiles.Status()
		if err != nil {
			return err
		}
		if status == nil {
			return fmt.Errorf("no experiment found")
		}
		output := formatStatus(status, resultFiles)
		var prefix string
		if lines > 0 {
			// Return to the block's start and erase it, including any obsolete lines.
			prefix = fmt.Sprintf("\x1b[%dA\r\x1b[J", lines)
		}
		// Progress output is best effort and must not affect experiment execution.
		_, _ = io.WriteString(w, prefix+output)
		width := 0
		if file, ok := w.(interface{ Fd() uintptr }); ok {
			width, _, _ = term.GetSize(int(file.Fd()))
		}
		lines = statusLines(output, width)
		return nil
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := redraw(); err != nil {
			// Wait for the worker before the caller closes logs or releases the lock.
			cancel()
			<-done
			return err
		}
		select {
		case err := <-done:
			return errors.Join(err, redraw())
		case <-ticker.C:
		}
	}
}
