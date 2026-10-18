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
	"uuid"

	"github.com/felixge/doe/internal/model"
)

// RunIndex holds reuse candidates from earlier experiments and appends new runs.
// The caller must hold the results lock for its entire lifetime.
type RunIndex struct {
	results *Results
	file    *os.File
	paths   map[string]string
}

type indexEntry struct {
	Hash string `json:"hash"`
	Path string `json:"path"`
}

// OpenRunIndex reads the index once. Later entries win because experiments run
// sequentially under the results lock. An incomplete final line is discarded
// before appending so an interrupted write cannot corrupt the next entry.
func (r *Results) OpenRunIndex() (*RunIndex, error) {
	path := filepath.Join(r.dir, "runs.index.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open run index %s: %w", path, err)
	}
	index := &RunIndex{results: r, file: file, paths: make(map[string]string)}
	data, err := io.ReadAll(file)
	if err == nil {
		complete := bytes.LastIndexByte(data, '\n') + 1
		for line := range bytes.SplitSeq(data[:complete], []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			var entry indexEntry
			if err = json.Unmarshal(line, &entry); err != nil {
				break
			}
			if entry.Hash == "" || !filepath.IsLocal(entry.Path) || filepath.Ext(entry.Path) != ".json" {
				err = fmt.Errorf("invalid run index entry: %s", line)
				break
			}
			index.paths[entry.Hash] = entry.Path
		}
		if err == nil && complete < len(data) {
			err = file.Truncate(int64(complete))
		}
	}
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("read run index %s: %w", path, err)
	}
	return index, nil
}

// Close releases the index file.
func (index *RunIndex) Close() error {
	return index.file.Close()
}

// Add indexes a newly executed successful run. Copies are never indexed, and
// new entries do not become candidates within the current experiment.
func (index *RunIndex) Add(hash string, run *model.Run) error {
	if run.State != model.RunStateDone {
		return fmt.Errorf("cannot index unfinished run %s", run.ID)
	}
	if run.Reused() {
		return fmt.Errorf("cannot index reused run %s", run.ID)
	}
	path, err := filepath.Rel(index.results.dir, index.results.runPath(run.ExperimentID, run.ID))
	if err != nil {
		return err
	}
	data, err := json.Marshal(indexEntry{Hash: hash, Path: filepath.ToSlash(path)})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	n, err := index.file.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return fmt.Errorf("append run index: %w", err)
	}
	return nil
}

// Reuse copies the latest successful candidate into an experiment, preserving
// its execution ID and origin. Missing candidates are misses.
func (index *RunIndex) Reuse(experimentID uuid.UUID, hash string) (bool, error) {
	path, ok := index.paths[hash]
	if !ok {
		return false, nil
	}
	path = filepath.Join(index.results.dir, filepath.FromSlash(path))
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read reuse candidate %s: %w", path, err)
	}
	var run model.Run
	// Preserve numeric values when rewriting arbitrary points and outcomes.
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&run); err != nil {
		return false, fmt.Errorf("decode reuse candidate %s: %w", path, err)
	}
	if run.State != model.RunStateDone {
		return false, nil
	}
	logPath := strings.TrimSuffix(path, ".json") + ".log"
	log, err := os.Open(logPath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open reuse log %s: %w", logPath, err)
	}
	defer func() { _ = log.Close() }()

	r := index.results
	if err := os.MkdirAll(r.runsDir(experimentID), 0755); err != nil {
		return false, err
	}
	destLogPath := r.runLogPath(experimentID, run.ID)
	destLog, err := openExclusiveLog(destLogPath)
	if err != nil {
		return false, fmt.Errorf("create reused log %s: %w", destLogPath, err)
	}
	_, copyErr := io.Copy(destLog, log)
	err = errors.Join(copyErr, destLog.Close())
	if !run.Reused() {
		run.OriginalExperimentID = run.ExperimentID
	}
	run.ExperimentID = experimentID
	// Publish JSON last so discovering a run always implies its log is complete.
	if err == nil {
		err = writeJSON(r.runPath(experimentID, run.ID), &run)
	}
	if err != nil {
		_ = os.Remove(destLogPath)
		return false, fmt.Errorf("copy reused run %s: %w", path, err)
	}
	return true, nil
}
