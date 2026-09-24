package results

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"uuid"

	"github.com/felixge/doe2/internal/jsonl"
	"github.com/felixge/doe2/internal/model"
)

// State describes an experiment's observed progress.
type State string

const (
	StateSetup   State = "Setup"
	StateRunning State = "Running"
	StateDone    State = "Done"
	StateStopped State = "Stopped"
	StateError   State = "Error"
)

// Status is a snapshot of the latest attempted experiment in a results directory.
type Status struct {
	ExperimentID uuid.UUID
	State        State
	Completed    int // Runs recorded without an error.
	Total        int // Planned runs; zero until the experiment record is written.
	Error        string
}

// Status reads a snapshot without creating or changing the results directory.
// A nil status means no experiment has been attempted there.
func (r *Results) Status() (*Status, error) {
	lock, err := r.readStatusLock()
	if err != nil {
		return nil, err
	}
	var id uuid.UUID
	if lock != nil {
		defer func() { _ = lock.file.Close() }()
		id = lock.experimentID
	}

	var latest *model.Experiment
	if err := jsonl.ScanFile(filepath.Join(r.dir, "experiments.jsonl"), func(experiment *model.Experiment) error {
		if lock == nil || experiment.ID == id {
			latest = experiment
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if lock == nil {
		if latest == nil {
			return nil, nil
		}
		id = latest.ID
	}
	status := &Status{ExperimentID: id}
	if latest != nil {
		status.Total = len(latest.Points) * latest.Replicates
		status.Error = latest.SetupError
		type runRecord struct {
			ExperimentID uuid.UUID `json:"experiment_id"`
			Error        string    `json:"error"`
		}
		if err := jsonl.ScanFile(filepath.Join(r.dir, "runs.jsonl"), func(run runRecord) error {
			if run.ExperimentID == id {
				if run.Error == "" {
					status.Completed++
				} else if status.Error == "" {
					status.Error = run.Error
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	switch {
	case status.Error != "":
		status.State = StateError
	case latest == nil && lock != nil && lock.running:
		status.State = StateSetup
	case lock != nil && lock.running:
		status.State = StateRunning
	case latest != nil && status.Completed == status.Total:
		status.State = StateDone
	default:
		status.State = StateStopped
	}
	return status, nil
}

type statusLock struct {
	file         *os.File
	experimentID uuid.UUID
	running      bool
}

// readStatusLock keeps a shared lock open while records are read, if available.
func (r *Results) readStatusLock() (_ *statusLock, err error) {
	path := r.lockPath()
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		if err != nil {
			_ = file.Close()
		}
	}()

	var running bool
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("check %s: %w", path, err)
		}
		running = true
	}
	// A writer may hold the lock while briefly replacing its ID.
	for attempt := 0; ; attempt++ {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek %s: %w", path, err)
		}
		id, err := readExperimentID(file)
		if err == nil {
			return &statusLock{file: file, experimentID: id, running: running}, nil
		}
		if !running || attempt == 2 {
			return nil, fmt.Errorf("read experiment ID from %s: %w", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
