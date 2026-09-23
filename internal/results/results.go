// Package results manages files in a results directory.
package results

// Results identifies the directory containing result files.
type Results struct {
	dir string
}

// New returns a Results for dir.
func New(dir string) *Results {
	return &Results{dir: dir}
}
