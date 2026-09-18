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
	write(".gitignore", "*.tmp\n!keep.tmp\nnested/ignored\n")
	write("data.txt", "data")
	write("drop.tmp", "drop")
	write("keep.tmp", "keep")
	write("nested/.gitignore", "local.log\n")
	write("nested/local.log", "log")
	write("nested/ignored/file", "ignored")
	write(".git/config", "git")
	write("results/runs.jsonl", "result")
	if err := os.Symlink("data.txt", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	s, err := Capture(root, filepath.Join(root, "results"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, path := range []string{".gitignore", "data.txt", "keep.tmp", "link", "nested/.gitignore"} {
		if _, ok := s.Files[path]; !ok {
			t.Errorf("expected %s in snapshot", path)
		}
	}
	for _, path := range []string{"drop.tmp", "nested/local.log", "nested/ignored/file", ".git/config", "results/runs.jsonl"} {
		if _, ok := s.Files[path]; ok {
			t.Errorf("did not expect %s in snapshot", path)
		}
	}

	destination := filepath.Join(t.TempDir(), "study")
	if err := s.CopyTo(destination); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(destination, "link"))
	if err != nil {
		t.Fatal(err)
	}
	if target != "data.txt" {
		t.Fatalf("link target = %q", target)
	}
}

func TestCaptureHashChangesWithSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink("one", link); err != nil {
		t.Fatal(err)
	}
	first, err := Capture(root, filepath.Join(root, "results"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("two", link); err != nil {
		t.Fatal(err)
	}
	second, err := Capture(root, filepath.Join(root, "results"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if first.Hash == second.Hash {
		t.Fatal("snapshot hash did not change")
	}
}

func TestGitignoreEscapes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("literal\\*.txt\nspace\\ \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"literal*.txt", "literal-one.txt", "space "} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Capture(root, filepath.Join(root, "results"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, ok := s.Files["literal*.txt"]; ok {
		t.Fatal("escaped wildcard pattern did not match literally")
	}
	if _, ok := s.Files["space "]; ok {
		t.Fatal("escaped trailing space pattern did not match")
	}
	if _, ok := s.Files["literal-one.txt"]; !ok {
		t.Fatal("escaped wildcard pattern acted as a wildcard")
	}
}
