package results

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"uuid"
)

func TestInitVersion(t *testing.T) {
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
			r, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			release, err := r.Lock(uuid.NewV7())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = release() }()
			path := filepath.Join(r.dir, versionFile)
			if tc.stored {
				if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.legacy {
				if err := os.Mkdir(filepath.Join(r.dir, "experiments"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			err = r.InitVersion()
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), r.dir) {
					t.Fatalf("InitVersion = %v, want %q and directory", err, tc.want)
				}
			} else if err != nil {
				t.Fatal(err)
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

func TestVersionIOErrors(t *testing.T) {
	t.Run("version is directory", func(t *testing.T) {
		r, err := New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(r.dir, versionFile), 0755); err != nil {
			t.Fatal(err)
		}
		if err := r.InitVersion(); err == nil || !strings.Contains(err.Error(), "read results database version") {
			t.Errorf("InitVersion = %v", err)
		}
		if _, err := r.Status(); err == nil || !strings.Contains(err.Error(), "read results database version") {
			t.Errorf("Status = %v", err)
		}
	})
	t.Run("missing directory cannot initialize", func(t *testing.T) {
		r := Open(filepath.Join(t.TempDir(), "missing"))
		if err := r.InitVersion(); err == nil || !strings.Contains(err.Error(), "write results database version") {
			t.Errorf("InitVersion = %v", err)
		}
	})
}

func TestStatusEmptyDirectoryIsReadOnly(t *testing.T) {
	for _, lock := range []bool{false, true} {
		t.Run(strconv.FormatBool(lock), func(t *testing.T) {
			r, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if lock {
				release, err := r.Lock(uuid.NewV7())
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
