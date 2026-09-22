package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCapture(t *testing.T) {
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "*.tmp\nnested/ignored\n")
	write("data.txt", "data")
	write("drop.tmp", "drop")
	write("nested/ignored/file", "included")
	write("nested/work/file", "included")
	write(".git/config", "git")
	write("results/runs.jsonl", "result")
	write("work/output.txt", "work")
	write("compression.study.yaml", "selected")
	write("other.study.yaml", "excluded")

	s, err := Capture(root, "compression.study.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		".gitignore",
		"data.txt",
		"drop.tmp",
		"nested/ignored/file",
		"nested/work/file",
		"compression.study.yaml",
	} {
		if _, ok := s.Files[path]; !ok {
			t.Errorf("expected %s in snapshot", path)
		}
	}
	for _, path := range []string{".git/config", "results/runs.jsonl", "work/output.txt", "other.study.yaml"} {
		if _, ok := s.Files[path]; ok {
			t.Errorf("did not expect %s in snapshot", path)
		}
	}
}

func TestCaptureHashesRegularFileContents(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("large file content\n", 1<<16)
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Capture(root, "compression.study.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(content))
	if got := s.Files["large.txt"]; got != hex.EncodeToString(want[:]) {
		t.Fatalf("large.txt hash = %q, want %q", got, hex.EncodeToString(want[:]))
	}
}

func TestCaptureRejectsSymlinks(t *testing.T) {
	tests := []struct {
		name   string
		target func(t *testing.T, root string) string
	}{
		{
			name: "file",
			target: func(t *testing.T, root string) string {
				writeTestFile(t, filepath.Join(root, "target.txt"), "data")
				return "target.txt"
			},
		},
		{
			name: "directory",
			target: func(t *testing.T, root string) string {
				if err := os.Mkdir(filepath.Join(root, "target"), 0o755); err != nil {
					t.Fatal(err)
				}
				return "target"
			},
		},
		{
			name: "external",
			target: func(t *testing.T, _ string) string {
				target := filepath.Join(t.TempDir(), "external.txt")
				writeTestFile(t, target, "data")
				return target
			},
		},
		{
			name: "broken",
			target: func(_ *testing.T, _ string) string {
				return "missing"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			link := filepath.Join(root, "nested", "input")
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(test.target(t, root), link); err != nil {
				t.Fatal(err)
			}
			_, err := Capture(root, "compression.study.yaml")
			if err == nil || err.Error() != "study input must not be a symlink: nested/input" {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestCaptureIgnoresSymlinksInExcludedTrees(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".git", "results", "work"} {
		path := filepath.Join(root, dir)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("missing", filepath.Join(path, "link")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Capture(root, "compression.study.yaml"); err != nil {
		t.Fatal(err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureHashIgnoresResultsAndWork(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"results/runs.jsonl", "work/output.txt"} {
		path := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	first, err := Capture(root, "compression.study.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"results/runs.jsonl", "work/output.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("after"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	second, err := Capture(root, "compression.study.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if first.Hash != second.Hash {
		t.Fatal("results or work changed the snapshot hash")
	}
}
