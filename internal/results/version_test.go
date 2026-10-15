package results

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"uuid"
)

func TestOpenVersion(t *testing.T) {
	for _, tc := range []struct {
		name, data, want string
		stored, legacy   bool
	}{
		{name: "empty"},
		{name: "current", stored: true, data: strconv.Itoa(databaseVersion) + "\n"},
		{name: "older", stored: true, data: "0\n", want: "older than supported"},
		{name: "legacy", legacy: true, want: "version 0 is older"},
		{name: "newer", stored: true, data: strconv.Itoa(databaseVersion+1) + "\n", want: "use a newer doe binary"},
		{name: "malformed", stored: true, data: "broken\n", want: "invalid results database version"},
		{name: "negative", stored: true, data: "-1\n", want: "invalid results database version"},
		{name: "blank", stored: true, data: "", want: "invalid results database version"},
		{name: "overflow", stored: true, data: "999999999999999999999999999999", want: "invalid results database version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, versionFile)
			if tc.stored {
				if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.legacy {
				if err := os.Mkdir(filepath.Join(dir, "experiments"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			r, release, err := Open(dir, uuid.NewV7(), false)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), dir) {
					t.Fatalf("Open = %v, want %q and directory", err, tc.want)
				}
				if r != nil || release != nil {
					t.Fatal("failed Open returned results or release function")
				}
				assertResultsUnlocked(t, dir)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = release() }()
				if r.dir != dir {
					t.Errorf("directory = %q, want %q", r.dir, dir)
				}
			}
			data, readErr := os.ReadFile(path)
			switch {
			case tc.stored:
				if readErr != nil || string(data) != tc.data {
					t.Errorf("existing version changed: %q, %v", data, readErr)
				}
			case tc.want != "":
				if !os.IsNotExist(readErr) {
					t.Errorf("rejection created version: %q, %v", data, readErr)
				}
			default:
				if readErr != nil || string(data) != strconv.Itoa(databaseVersion)+"\n" {
					t.Errorf("initialized version = %q, %v", data, readErr)
				}
			}
		})
	}
}

func assertResultsUnlocked(t *testing.T, dir string) {
	t.Helper()
	release, err := OpenReadOnly(dir).lock(uuid.NewV7())
	if err != nil {
		t.Fatalf("constructor left results locked: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenVersionIOErrorReleasesLock(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, versionFile), 0755); err != nil {
		t.Fatal(err)
	}
	r, release, err := Open(dir, uuid.NewV7(), false)
	if err == nil || !strings.Contains(err.Error(), "read results database version") {
		t.Fatalf("Open = %v", err)
	}
	if r != nil || release != nil {
		t.Fatal("failed Open returned results or release function")
	}
	assertResultsUnlocked(t, dir)
	if _, err := OpenReadOnly(dir).Status(); err == nil || !strings.Contains(err.Error(), "read results database version") {
		t.Errorf("Status = %v", err)
	}
}

func TestOpenPermissionErrorsReleaseLock(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	for _, clean := range []bool{false, true} {
		t.Run(strconv.FormatBool(clean), func(t *testing.T) {
			dir := t.TempDir()
			// Pre-create the lock so the constructor can reach the failing step.
			assertResultsUnlocked(t, dir)
			blocked := dir
			want := "write results database version"
			if clean {
				blocked = filepath.Join(dir, "blocked")
				if err := os.Mkdir(blocked, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(blocked, "old"), []byte("old"), 0600); err != nil {
					t.Fatal(err)
				}
				want = "remove result"
			}
			if err := os.Chmod(blocked, 0500); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.Chmod(blocked, 0755) }()
			r, release, err := Open(dir, uuid.NewV7(), clean)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("Open = %v, want %q", err, want)
			}
			if r != nil || release != nil {
				t.Fatal("failed Open returned results or release function")
			}
			assertResultsUnlocked(t, dir)
		})
	}
}

func TestOpenCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "results")
	r, release, err := Open(dir, uuid.NewV7(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	data, err := os.ReadFile(filepath.Join(r.dir, versionFile))
	if err != nil || string(data) != strconv.Itoa(databaseVersion)+"\n" {
		t.Errorf("initialized version = %q, %v", data, err)
	}
}

func TestStatusEmptyDirectoryIsReadOnly(t *testing.T) {
	for _, lock := range []bool{false, true} {
		t.Run(strconv.FormatBool(lock), func(t *testing.T) {
			r := OpenReadOnly(t.TempDir())
			if lock {
				release, err := r.lock(uuid.NewV7())
				if err != nil {
					t.Fatal(err)
				}
				if err := release(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := r.Status(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(r.dir, versionFile)); !os.IsNotExist(err) {
				t.Errorf("Status created version: %v", err)
			}
		})
	}
}
