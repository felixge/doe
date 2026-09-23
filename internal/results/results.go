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

// New returns a Results for dir.
func New(dir string) *Results {
	return &Results{dir: dir}
}

// AppendExperiment records an experiment as a JSON line, creating the results directory if needed.
func (r *Results) AppendExperiment(experiment model.Experiment) error {
	if err := os.MkdirAll(r.dir, 0755); err != nil {
		return fmt.Errorf("create results directory: %w", err)
	}
	return jsonl.AppendFile(filepath.Join(r.dir, "experiments.jsonl"), experiment)
}
