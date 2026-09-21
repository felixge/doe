package snapshot

import (
	"os"
	"path/filepath"
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
	if err := os.Symlink("data.txt", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	s, err := Capture(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		".gitignore",
		"data.txt",
		"drop.tmp",
		"link",
		"nested/ignored/file",
		"nested/work/file",
	} {
		if _, ok := s.Files[path]; !ok {
			t.Errorf("expected %s in snapshot", path)
		}
	}
	for _, path := range []string{".git/config", "results/runs.jsonl", "work/output.txt"} {
		if _, ok := s.Files[path]; ok {
			t.Errorf("did not expect %s in snapshot", path)
		}
	}
}

func TestCaptureHashChangesWithSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink("one", link); err != nil {
		t.Fatal(err)
	}
	first, err := Capture(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("two", link); err != nil {
		t.Fatal(err)
	}
	second, err := Capture(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.Hash == second.Hash {
		t.Fatal("snapshot hash did not change")
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
	first, err := Capture(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"results/runs.jsonl", "work/output.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("after"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	second, err := Capture(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.Hash != second.Hash {
		t.Fatal("results or work changed the snapshot hash")
	}
}
