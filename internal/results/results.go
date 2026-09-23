// Package results manages files in a results directory.
package results

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"uuid"

	"github.com/felixge/doe2/internal/jsonl"
	"github.com/felixge/doe2/internal/model"
)

// Results identifies the directory containing result files.
type Results struct {
	dir string
}

// New creates the results directory and returns a Results for dir.
func New(dir string) (*Results, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create results directory: %w", err)
	}
	return &Results{dir: dir}, nil
}

// Dir returns the results directory.
func (r *Results) Dir() string {
	return r.dir
}

// LockExperiment writes the experiment ID and holds the shared results lock
// until the returned release function is called. Only one experiment can run per directory.
func (r *Results) LockExperiment(id uuid.UUID) (func() error, error) {
	path := r.lockPath()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			runningID, readErr := readExperimentID(file)
			_ = file.Close()
			if readErr != nil {
				return nil, fmt.Errorf("read running experiment from %s: %w", path, readErr)
			}
			return nil, fmt.Errorf("another experiment is running: %s", runningID)
		}
		_ = file.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	if err := file.Truncate(0); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("truncate %s: %w", path, err)
	}
	if _, err := file.WriteString(id.String() + "\n"); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return file.Close, nil
}

func (r *Results) lockPath() string {
	return filepath.Join(r.dir, "experiment.lock")
}

func readExperimentID(file *os.File) (uuid.UUID, error) {
	data, err := io.ReadAll(file)
	if err != nil {
		return uuid.UUID{}, err
	}
	return uuid.Parse(strings.TrimSpace(string(data)))
}

// AppendExperiment records an experiment as a JSON line.
func (r *Results) AppendExperiment(experiment *model.Experiment) error {
	return jsonl.AppendFile(filepath.Join(r.dir, "experiments.jsonl"), experiment)
}
