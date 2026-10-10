package cmd

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// snapshotResults excludes the lock, which run updates before checking versions.
func snapshotResults(t *testing.T) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir("results", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() == "results.lock" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestDatabaseVersionRejectionAndClean(t *testing.T) {
	for _, tc := range []struct {
		name, version, want string
		missing             bool
	}{
		{name: "older", version: "0\n", want: "older than supported"},
		{name: "unversioned", missing: true, want: "version 0 is older"},
		{name: "newer", version: "999\n", want: "use a newer doe binary"},
		{name: "malformed", version: "broken\n", want: "invalid results database version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			args := []string{"run", "foo=1", "-r", "echo '{}'; true '{foo}'"}
			code, stdout, stderr := runCommand(context.Background(), args...)
			if code != 0 || stderr != "" {
				t.Fatalf("initial run = %d, %q, %q", code, stdout, stderr)
			}
			original, _ := readCompletedExperiment(t, "results", stdout)
			versionPath := filepath.Join("results", "version")
			current, err := os.ReadFile(versionPath)
			if err != nil {
				t.Fatal(err)
			}
			lockPath := filepath.Join("results", "results.lock")
			lockInfo, err := os.Stat(lockPath)
			if err != nil {
				t.Fatal(err)
			}
			if tc.missing {
				if err := os.Remove(versionPath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(versionPath, []byte(tc.version), 0600); err != nil {
				t.Fatal(err)
			}
			before := snapshotResults(t)

			// Status must reject incompatible data without changing any files.
			lockData, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatal(err)
			}
			code, stdout, stderr = runCommand(context.Background(), "status")
			if code != 1 || stdout != "" || !strings.Contains(stderr, tc.want) {
				t.Errorf("status = %d, %q, %q; want %q", code, stdout, stderr, tc.want)
			}
			if tc.missing || tc.version == "0\n" {
				if !strings.Contains(stderr, "doe run with -c") || !strings.Contains(stderr, "delete previous results") {
					t.Errorf("status lacks reset instructions: %q", stderr)
				}
			}
			afterLock, err := os.ReadFile(lockPath)
			if err != nil || string(afterLock) != string(lockData) {
				t.Errorf("status changed lock: %q, %v", afterLock, err)
			}
			if after := snapshotResults(t); !reflect.DeepEqual(before, after) {
				t.Error("status changed results")
			}

			args = []string{"run", "foo=1", "-s", "touch setup-started; echo '{}'", "-r", "touch run-started; echo '{}'; true '{foo}'"}
			for _, noReuse := range []bool{false, true} {
				runArgs := append([]string{}, args...)
				if noReuse {
					runArgs = append(runArgs, "--no-reuse")
				}
				code, stdout, stderr = runCommand(context.Background(), runArgs...)
				if code != 1 || stdout != "" || !strings.Contains(stderr, tc.want) {
					t.Errorf("run (no-reuse=%v) = %d, %q, %q", noReuse, code, stdout, stderr)
				}
				if after := snapshotResults(t); !reflect.DeepEqual(before, after) {
					t.Error("rejection changed results")
				}
			}
			for _, path := range []string{"setup-started", "run-started"} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("rejected run executed script: %s, %v", path, err)
				}
			}

			code, _, stderr = runCommand(context.Background(), append(args, "--design", "-c")...)
			if code != 0 || stderr != "" {
				t.Errorf("design = %d, %q", code, stderr)
			}
			if after := snapshotResults(t); !reflect.DeepEqual(before, after) {
				t.Error("design changed results")
			}

			code, stdout, stderr = runCommand(context.Background(), append(args, "-c")...)
			if code != 0 || stderr != "" {
				t.Fatalf("clean = %d, %q, %q", code, stdout, stderr)
			}
			readCompletedExperiment(t, "results", stdout)
			data, err := os.ReadFile(versionPath)
			if err != nil || string(data) != string(current) {
				t.Errorf("reset version = %q, %v; want %q", data, err, current)
			}
			afterInfo, err := os.Stat(lockPath)
			if err != nil || !os.SameFile(lockInfo, afterInfo) {
				t.Errorf("clean replaced lock: %v", err)
			}
			if _, err := os.Stat(filepath.Join("results", "experiments", original.ID.String())); !os.IsNotExist(err) {
				t.Errorf("clean kept original experiment: %v", err)
			}
			for _, path := range []string{"setup-started", "run-started"} {
				if _, err := os.Stat(path); err != nil {
					t.Errorf("clean did not execute script: %s, %v", path, err)
				}
			}
		})
	}
}
