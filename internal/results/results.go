// Package results manages files in a results directory.
package results

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"uuid"

	"github.com/felixge/doe/internal/jsonl"
	"github.com/felixge/doe/internal/model"
)

const experimentLockFile = "experiment.lock"

// Results identifies the directory containing result files.
type Results struct {
	dir string
}

// New creates the results directory and returns a Results for dir.
func New(dir string) (*Results, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create results directory: %w", err)
	}
	return Open(dir), nil
}

// Open returns Results for dir without creating it. The directory may be absent.
func Open(dir string) *Results {
	return &Results{dir: dir}
}

// Dir returns the results directory.
func (r *Results) Dir() string {
	return r.dir
}

// ProjectDir returns the directory containing the results directory.
func (r *Results) ProjectDir() string {
	return filepath.Dir(r.dir)
}

// Clean removes previous results while preserving the lock file and directory.
// The caller must hold the experiment lock so another process cannot write here.
func (r *Results) Clean() error {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return fmt.Errorf("read results directory: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == experimentLockFile {
			continue
		}
		path := filepath.Join(r.dir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove result %s: %w", path, err)
		}
	}
	return nil
}

// SetupLogPath returns the path to an experiment's setup log.
func (r *Results) SetupLogPath(experimentID uuid.UUID) string {
	return r.logPath(experimentID, "setup")
}

// CreateSetupLog creates a log for an experiment's setup script.
func (r *Results) CreateSetupLog(experimentID uuid.UUID) (*os.File, error) {
	return r.createLog(r.SetupLogPath(experimentID), "setup")
}

// CreateRunLog creates a log for a run and returns it for streaming output.
func (r *Results) CreateRunLog(runID uuid.UUID) (*os.File, error) {
	return r.createLog(r.runLogPath(runID), "run")
}

func (r *Results) createLog(path, kind string) (*os.File, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create logs directory %s: %w", dir, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("open %s log %s: %w", kind, path, err)
	}
	return file, nil
}

// SetupEnv reads an optional JSON object on the setup log's last line.
func (r *Results) SetupEnv(experimentID uuid.UUID) (model.Env, error) {
	path := r.SetupLogPath(experimentID)
	last, err := lastLogLine(path, "setup")
	if err != nil {
		return nil, err
	}
	var environment model.Env
	if json.Unmarshal(last, &environment) != nil || environment == nil {
		return model.Env{}, nil
	}
	return environment, nil
}

// RunOutcome reads the final log line as a JSON object. Earlier lines may contain diagnostics.
func (r *Results) RunOutcome(runID uuid.UUID) (model.Outcome, error) {
	path := r.runLogPath(runID)
	last, err := lastLogLine(path, "run")
	if err != nil {
		return nil, err
	}
	var outcome model.Outcome
	if err := json.Unmarshal(last, &outcome); err != nil {
		return nil, fmt.Errorf("run log %s has no JSON object on its last line: %w", path, err)
	}
	if outcome == nil {
		return nil, fmt.Errorf("run log %s has no JSON object on its last line", path)
	}
	return outcome, nil
}

func lastLogLine(path, kind string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s log %s: %w", kind, path, err)
	}
	defer func() { _ = file.Close() }()

	reader := bufio.NewReader(file)
	var last []byte
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			last = line
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return nil, fmt.Errorf("read %s log %s: %w", kind, path, readErr)
			}
			break
		}
	}
	return last, nil
}

func (r *Results) runLogPath(runID uuid.UUID) string {
	return r.logPath(runID, "run")
}

func (r *Results) logPath(id uuid.UUID, kind string) string {
	return filepath.Join(r.dir, "logs", id.String()+"."+kind+".log")
}

// AppendRun records a run attempt as a JSON line.
func (r *Results) AppendRun(run *model.Run) error {
	return jsonl.AppendFile(filepath.Join(r.dir, "runs.jsonl"), run)
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
	return filepath.Join(r.dir, experimentLockFile)
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
