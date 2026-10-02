package results

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
	"uuid"

	"github.com/felixge/doe/internal/model"
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

// Status is a snapshot of the selected experiment in a results directory.
// A zero duration can mean unavailable; nonzero durations may display as 0s.
type Status struct {
	ExperimentID      uuid.UUID
	State             State
	Completed         int
	Total             int
	RunReplicate      int
	RunPoint          model.Point
	RunElapsed        time.Duration
	ExperimentElapsed time.Duration
	Remaining         time.Duration
	Error             string
}

// Status reads a snapshot without creating or changing the results directory.
// A nil status means no experiment has been attempted there.
func (r *Results) Status() (*Status, error) {
	return r.statusAt(time.Now())
}

func (r *Results) statusAt(now time.Time) (*Status, error) {
	lock, err := r.readStatusLock()
	if err != nil {
		return nil, err
	}
	if lock == nil {
		return nil, nil
	}
	defer func() { _ = lock.file.Close() }()

	status := &Status{ExperimentID: lock.experimentID}
	experiment, err := r.ReadExperiment(lock.experimentID)
	if errors.Is(err, os.ErrNotExist) {
		if lock.running {
			status.State = StateSetup
		} else {
			status.State = StateStopped
		}
		return status, nil
	}
	if err != nil {
		return nil, err
	}

	status.Total = len(experiment.Points) * experiment.Replicates
	status.Error = experiment.Error
	runs, err := r.Runs(experiment.ID)
	if err != nil {
		return nil, err
	}

	// Runs are appended in schedule order, so the i-th run executes the
	// point at sequence[i].
	sequence := flatSchedule(experiment)
	var active *model.Run
	samples := make(map[int][]time.Duration)
	for i, run := range runs {
		switch run.State {
		case model.StateDone:
			status.Completed++
			point := sequence[i]
			samples[point] = append(samples[point], run.End.Sub(run.Start))
		case model.StateRunning:
			active = run
		case model.StateError:
			if status.Error == "" {
				status.Error = run.Error
			}
		}
	}

	status.State = observedState(experiment.State, lock.running)
	if status.Error != "" && status.State == StateRunning {
		status.State = StateError
	}
	if status.State == StateRunning && active != nil {
		status.RunReplicate = active.Replicate
		status.RunPoint = active.Point
		if !active.Start.IsZero() && !now.Before(active.Start) {
			status.RunElapsed = now.Sub(active.Start)
		}
		status.Remaining, _ = model.EstimateTotalDuration(sequence[len(runs)-1:], samples, status.RunElapsed)
	}

	if !experiment.Start.IsZero() {
		end := experiment.End
		if end.IsZero() && lock.running {
			end = now
		}
		if !end.IsZero() && !end.Before(experiment.Start) {
			status.ExperimentElapsed = end.Sub(experiment.Start)
		}
	}
	return status, nil
}

func observedState(recorded model.State, running bool) State {
	if !running && (recorded == model.StateSetup || recorded == model.StateRunning) {
		return StateStopped
	}
	switch recorded {
	case model.StateSetup:
		return StateSetup
	case model.StateRunning:
		return StateRunning
	case model.StateDone:
		return StateDone
	case model.StateError:
		return StateError
	case model.StateStopped:
		return StateStopped
	default:
		return StateStopped
	}
}

// flatSchedule flattens the experiment's schedule into point indexes in run order.
func flatSchedule(experiment *model.Experiment) []int {
	sequence := make([]int, 0, len(experiment.Points)*experiment.Replicates)
	for _, row := range model.Schedule(len(experiment.Points), experiment.Replicates) {
		sequence = append(sequence, row...)
	}
	return sequence
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
	for attempt := 0; ; attempt++ {
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
