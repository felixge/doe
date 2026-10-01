// Package results manages files in a results directory.
package results

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"uuid"

	"github.com/felixge/doe/internal/model"
)

const resultsLockFile = "results.lock"

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

// Lock writes the experiment ID and holds the shared results lock until the
// returned release function is called. Only one experiment can run per directory.
func (r *Results) Lock(id uuid.UUID) (func() error, error) {
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
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("seek %s: %w", path, err)
	}
	if _, err := file.WriteString(id.String() + "\n"); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return file.Close, nil
}

// Clean removes previous results while preserving the lock file and directory.
// The caller must hold the results lock so another process cannot write here.
func (r *Results) Clean() error {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return fmt.Errorf("read results directory: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == resultsLockFile {
			continue
		}
		path := filepath.Join(r.dir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove result %s: %w", path, err)
		}
	}
	return nil
}

// CreateExperiment exclusively creates an experiment record and its setup log.
// The returned read/write log is owned by the caller.
func (r *Results) CreateExperiment(experiment *model.Experiment) (*os.File, error) {
	dir := r.experimentDir(experiment.ID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create experiment directory %s: %w", dir, err)
	}
	logPath := r.SetupLogPath(experiment.ID)
	log, err := openExclusiveLog(logPath)
	if err != nil {
		return nil, fmt.Errorf("create setup log %s: %w", logPath, err)
	}
	// The exclusive log open above guarantees the ID is new, so the record
	// can be written with a plain replace.
	recordPath := r.experimentPath(experiment.ID)
	if err := writeJSON(recordPath, experiment); err != nil {
		_ = log.Close()
		_ = os.Remove(logPath)
		return nil, fmt.Errorf("create experiment record %s: %w", recordPath, err)
	}
	return log, nil
}

// UpdateExperiment atomically replaces an existing complete experiment record.
func (r *Results) UpdateExperiment(experiment *model.Experiment) error {
	path := r.experimentPath(experiment.ID)
	if err := updateJSON(path, experiment); err != nil {
		return fmt.Errorf("update experiment record %s: %w", path, err)
	}
	return nil
}

// ReadExperiment reads one experiment record.
func (r *Results) ReadExperiment(id uuid.UUID) (*model.Experiment, error) {
	path := r.experimentPath(id)
	experiment, err := readJSON[model.Experiment](path)
	if err != nil {
		return nil, fmt.Errorf("read experiment record %s: %w", path, err)
	}
	return experiment, nil
}

// CreateRun exclusively creates a shared run record and its log. It does not
// add the run to an experiment's membership file.
func (r *Results) CreateRun(run *model.Run) (*os.File, error) {
	dir := filepath.Join(r.dir, "runs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create runs directory %s: %w", dir, err)
	}
	logPath := r.runLogPath(run.ID)
	log, err := openExclusiveLog(logPath)
	if err != nil {
		return nil, fmt.Errorf("create run log %s: %w", logPath, err)
	}
	// The exclusive log open above guarantees the ID is new, so the record
	// can be written with a plain replace.
	recordPath := r.runPath(run.ID)
	if err := writeJSON(recordPath, run); err != nil {
		_ = log.Close()
		_ = os.Remove(logPath)
		return nil, fmt.Errorf("create run record %s: %w", recordPath, err)
	}
	return log, nil
}

// UpdateRun atomically replaces an existing complete run record.
func (r *Results) UpdateRun(run *model.Run) error {
	path := r.runPath(run.ID)
	if err := updateJSON(path, run); err != nil {
		return fmt.Errorf("update run record %s: %w", path, err)
	}
	return nil
}

// ReadRun reads one shared run record.
func (r *Results) ReadRun(id uuid.UUID) (*model.Run, error) {
	path := r.runPath(id)
	run, err := readJSON[model.Run](path)
	if err != nil {
		return nil, fmt.Errorf("read run record %s: %w", path, err)
	}
	return run, nil
}

// AppendRun appends a run reference to an experiment in insertion order.
func (r *Results) AppendRun(experimentID, runID uuid.UUID) error {
	if _, err := r.ReadExperiment(experimentID); err != nil {
		return err
	}
	if _, err := r.ReadRun(runID); err != nil {
		return err
	}
	path := r.runIDsPath(experimentID)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open run membership %s: %w", path, err)
	}
	line := runID.String() + "\n"
	n, writeErr := file.WriteString(line)
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("append run membership %s: %w", path, writeErr)
	}
	if n != len(line) {
		return fmt.Errorf("append run membership %s: %w", path, io.ErrShortWrite)
	}
	if closeErr != nil {
		return fmt.Errorf("close run membership %s: %w", path, closeErr)
	}
	return nil
}

// Runs reads an experiment's referenced runs in insertion order. An
// unterminated final membership line is ignored.
func (r *Results) Runs(experimentID uuid.UUID) ([]*model.Run, error) {
	if _, err := r.ReadExperiment(experimentID); err != nil {
		return nil, err
	}
	path := r.runIDsPath(experimentID)
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return []*model.Run{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open run membership %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	var runs []*model.Run
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, fmt.Errorf("read run membership %s: %w", path, readErr)
		}
		if errors.Is(readErr, io.EOF) {
			return runs, nil
		}
		id, err := uuid.Parse(strings.TrimSpace(string(line)))
		if err != nil {
			return nil, fmt.Errorf("parse run membership %s: %w", path, err)
		}
		run, err := r.ReadRun(id)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
}

// SetupLogPath returns the path to an experiment's setup log.
func (r *Results) SetupLogPath(experimentID uuid.UUID) string {
	return filepath.Join(r.experimentDir(experimentID), "setup.log")
}

func (r *Results) experimentDir(id uuid.UUID) string {
	return filepath.Join(r.dir, "experiments", id.String())
}

func (r *Results) experimentPath(id uuid.UUID) string {
	return filepath.Join(r.experimentDir(id), "experiment.json")
}

func (r *Results) runIDsPath(id uuid.UUID) string {
	return filepath.Join(r.experimentDir(id), "runs.ids")
}

func (r *Results) runPath(id uuid.UUID) string {
	return filepath.Join(r.dir, "runs", id.String()+".json")
}

func (r *Results) runLogPath(id uuid.UUID) string {
	return filepath.Join(r.dir, "runs", id.String()+".log")
}

func (r *Results) lockPath() string {
	return filepath.Join(r.dir, resultsLockFile)
}

func openExclusiveLog(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0644)
}

// updateJSON replaces an existing record. The results lock makes the
// existence check race-free because only one process writes records.
func updateJSON(path string, value any) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return writeJSON(path, value)
}

// writeJSON writes a temp file and renames it into place so readers never
// observe a partial record.
func writeJSON(path string, value any) error {
	data, err := marshalJSON(value)
	if err != nil {
		return err
	}
	temp, err := writeTemp(path, data)
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	defer func() { _ = os.Remove(tempPath) }()
	return os.Rename(tempPath, path)
}

func marshalJSON(value any) ([]byte, error) {
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return data.Bytes(), nil
}

func writeTemp(path string, data []byte) (*os.File, error) {
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return nil, err
	}
	if err := temp.Chmod(0644); err != nil {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		return nil, err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		return nil, err
	}
	return temp, nil
}

func readJSON[T any](path string) (*T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func readExperimentID(file *os.File) (uuid.UUID, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return uuid.UUID{}, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return uuid.UUID{}, err
	}
	return uuid.Parse(strings.TrimSpace(string(data)))
}
