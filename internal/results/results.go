// Package results manages files in a results directory.
package results

import (
	"fmt"
	"os"
	"path/filepath"

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

// AppendExperiment records an experiment as a JSON line.
func (r *Results) AppendExperiment(experiment model.Experiment) error {
	return jsonl.AppendFile(filepath.Join(r.dir, "experiments.jsonl"), experiment)
}
