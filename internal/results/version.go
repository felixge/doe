package results

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// databaseVersion must increase when stored results change incompatibly.
// Only this version is supported; there are no migrations.
const databaseVersion = 1

const versionFile = "version"

// InitVersion checks existing results or initializes an empty directory.
// The caller must hold the results lock. Clean must run first for a reset.
func (r *Results) InitVersion() error {
	version, exists, err := r.readVersion()
	if err != nil {
		return err
	}
	if err := r.checkVersion(version); err != nil {
		return err
	}
	if exists {
		return nil
	}
	path := filepath.Join(r.dir, versionFile)
	if err := writeFile(path, []byte(strconv.Itoa(databaseVersion)+"\n")); err != nil {
		return fmt.Errorf("write results database version %s: %w", path, err)
	}
	return nil
}

func (r *Results) checkVersion(version int) error {
	if version < databaseVersion {
		return fmt.Errorf("results database %s: version %d is older than supported version %d; rerun doe run with -c to delete previous results", r.dir, version, databaseVersion)
	}
	if version > databaseVersion {
		return fmt.Errorf("results database %s: version %d is newer than supported version %d; use a newer doe binary", r.dir, version, databaseVersion)
	}
	return nil
}

// readVersion treats unversioned results as version 0, but permits empty or
// absent directories. The lock file alone does not count as stored results.
func (r *Results) readVersion() (version int, exists bool, err error) {
	path := filepath.Join(r.dir, versionFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		entries, err := os.ReadDir(r.dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, false, fmt.Errorf("read results directory %s: %w", r.dir, err)
		}
		for _, entry := range entries {
			if entry.Name() != resultsLockFile {
				return 0, false, nil
			}
		}
		return databaseVersion, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read results database version %s: %w", path, err)
	}
	version, err = strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || version < 0 {
		return 0, true, fmt.Errorf("invalid results database version in %s: %q", path, strings.TrimSpace(string(data)))
	}
	return version, true, nil
}
