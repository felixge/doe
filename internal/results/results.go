// Package results manages files in a results directory.
package results

import (
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

// Open creates and locks the results directory for an experiment, optionally
// removes previous results, then checks or initializes the database version.
// The caller must call release when the experiment ends. Errors release the lock.
func Open(dir string, id uuid.UUID, clean bool) (*Results, func() error, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, nil, fmt.Errorf("create results directory: %w", err)
	}
	r := OpenReadOnly(dir)
	release, err := r.lock(id)
	if err != nil {
		return nil, nil, err
	}
	if clean {
		err = r.clean()
	}
	if err == nil {
		err = r.initVersion()
	}
	if err != nil {
		_ = release()
		return nil, nil, err
	}
	return r, release, nil
}

// OpenReadOnly returns a handle for reading results without creating or locking
// the directory. The directory may be absent. Status manages its own shared lock.
func OpenReadOnly(dir string) *Results {
	return &Results{dir: dir}
}

// lock writes the experiment ID and holds the exclusive results lock until the
// returned release function is called. Only one experiment can run per directory.
func (r *Results) lock(id uuid.UUID) (func() error, error) {
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

// clean removes previous results while preserving the lock file and directory.
// The caller must hold the results lock so another process cannot write here.
func (r *Results) clean() error {
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
	dir := r.ExperimentDir(experiment.ID)
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

// CreateRun exclusively creates a run record and its log within its experiment.
func (r *Results) CreateRun(run *model.Run) (*os.File, error) {
	dir := r.runsDir(run.ExperimentID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create runs directory %s: %w", dir, err)
	}
	logPath := r.runLogPath(run.ExperimentID, run.ID)
	log, err := openExclusiveLog(logPath)
	if err != nil {
		return nil, fmt.Errorf("create run log %s: %w", logPath, err)
	}
	// The exclusive log open above guarantees the ID is new, so the record
	// can be written with a plain replace.
	recordPath := r.runPath(run.ExperimentID, run.ID)
	if err := writeJSON(recordPath, run); err != nil {
		_ = log.Close()
		_ = os.Remove(logPath)
		return nil, fmt.Errorf("create run record %s: %w", recordPath, err)
	}
	return log, nil
}

// UpdateRun atomically replaces an existing complete run record.
func (r *Results) UpdateRun(run *model.Run) error {
	path := r.runPath(run.ExperimentID, run.ID)
	if err := updateJSON(path, run); err != nil {
		return fmt.Errorf("update run record %s: %w", path, err)
	}
	return nil
}

// ReadRun reads one run record from an experiment's directory.
func (r *Results) ReadRun(experimentID, runID uuid.UUID) (*model.Run, error) {
	path := r.runPath(experimentID, runID)
	run, err := readJSON[model.Run](path)
	if err != nil {
		return nil, fmt.Errorf("read run record %s: %w", path, err)
	}
	return run, nil
}

// Runs reads the JSON run records in an experiment's directory, sorted by filename.
// File order need not match execution order.
func (r *Results) Runs(experimentID uuid.UUID) ([]*model.Run, error) {
	if _, err := r.ReadExperiment(experimentID); err != nil {
		return nil, err
	}
	dir := r.runsDir(experimentID)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []*model.Run{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read runs directory %s: %w", dir, err)
	}
	runs := make([]*model.Run, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		run, err := readJSON[model.Run](path)
		if err != nil {
			return nil, fmt.Errorf("read run record %s: %w", path, err)
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// SetupLogPath returns the path to an experiment's setup log.
func (r *Results) SetupLogPath(experimentID uuid.UUID) string {
	return filepath.Join(r.ExperimentDir(experimentID), "setup.log")
}

// CreateReportLog exclusively creates an experiment's report log.
// The caller closes the returned log.
func (r *Results) CreateReportLog(experimentID uuid.UUID) (*os.File, error) {
	path := r.ReportLogPath(experimentID)
	log, err := openExclusiveLog(path)
	if err != nil {
		return nil, fmt.Errorf("create report log %s: %w", path, err)
	}
	return log, nil
}

// ReportLogPath returns the path to an experiment's report log.
func (r *Results) ReportLogPath(experimentID uuid.UUID) string {
	return filepath.Join(r.ExperimentDir(experimentID), "report.log")
}

// ExperimentDir returns the path to an experiment's directory.
func (r *Results) ExperimentDir(id uuid.UUID) string {
	return filepath.Join(r.dir, "experiments", id.String())
}

func (r *Results) experimentPath(id uuid.UUID) string {
	return filepath.Join(r.ExperimentDir(id), "experiment.json")
}

func (r *Results) runsDir(experimentID uuid.UUID) string {
	return filepath.Join(r.ExperimentDir(experimentID), "runs")
}

func (r *Results) runPath(experimentID, runID uuid.UUID) string {
	return filepath.Join(r.runsDir(experimentID), runID.String()+".json")
}

func (r *Results) runLogPath(experimentID, runID uuid.UUID) string {
	return filepath.Join(r.runsDir(experimentID), runID.String()+".log")
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
	return writeFile(path, data)
}

// writeFile publishes bytes atomically.
func writeFile(path string, data []byte) error {
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
