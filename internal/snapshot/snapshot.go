package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Snapshot is an immutable copy of the files that make up a study.
type Snapshot struct {
	Files map[string]string
	Hash  string

	dir string
}

// Capture copies and hashes a study before its setup command can change it.
func Capture(root, output string) (*Snapshot, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return nil, err
	}
	excludedOutput := ""
	if rel, err := filepath.Rel(root, output); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		excludedOutput = filepath.ToSlash(rel)
	}

	tmp, err := os.MkdirTemp("", "doe-study-")
	if err != nil {
		return nil, err
	}
	s := &Snapshot{Files: map[string]string{}, dir: tmp}
	if err := s.walk(root, "", excludedOutput, nil); err != nil {
		s.Close()
		return nil, err
	}

	paths := make([]string, 0, len(s.Files))
	for path := range s.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, path := range paths {
		_, _ = h.Write([]byte(path))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(s.Files[path]))
		_, _ = h.Write([]byte{0})
	}
	s.Hash = hex.EncodeToString(h.Sum(nil))
	return s, nil
}

func (s *Snapshot) walk(root, rel, excludedOutput string, parentRules []ignoreRule) error {
	abs := filepath.Join(root, filepath.FromSlash(rel))
	entries, err := os.ReadDir(abs)
	if err != nil {
		return err
	}
	rules := append([]ignoreRule(nil), parentRules...)
	if data, err := os.ReadFile(filepath.Join(abs, ".gitignore")); err == nil {
		local, err := parseIgnore(rel, data)
		if err != nil {
			return fmt.Errorf("parse %s: %w", filepath.Join(abs, ".gitignore"), err)
		}
		rules = append(rules, local...)
	} else if !os.IsNotExist(err) {
		return err
	}

	for _, entry := range entries {
		path := entry.Name()
		if rel != "" {
			path = rel + "/" + path
		}
		if entry.IsDir() && entry.Name() == ".git" {
			continue
		}
		if path == "results" || strings.HasPrefix(path, "results/") ||
			(excludedOutput != "" && (path == excludedOutput || strings.HasPrefix(path, excludedOutput+"/"))) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if ignored(path, info.IsDir(), rules) {
			continue
		}
		if info.IsDir() {
			if err := s.walk(root, path, excludedOutput, rules); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() && info.Mode()&fs.ModeSymlink == 0 {
			return fmt.Errorf("unsupported study file %s (%s)", path, info.Mode().Type())
		}
		if err := s.copyEntry(root, path, info); err != nil {
			return err
		}
	}
	return nil
}

func (s *Snapshot) copyEntry(root, path string, info fs.FileInfo) error {
	source := filepath.Join(root, filepath.FromSlash(path))
	destination := filepath.Join(s.dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	var content []byte
	var err error
	if info.Mode()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(source)
		if err != nil {
			return err
		}
		content = []byte(target)
		if err := os.Symlink(target, destination); err != nil {
			return err
		}
	} else {
		content, err = os.ReadFile(source)
		if err != nil {
			return err
		}
		if err := os.WriteFile(destination, content, info.Mode().Perm()); err != nil {
			return err
		}
	}
	h := sha256.Sum256(content)
	s.Files[path] = hex.EncodeToString(h[:])
	return nil
}

// CopyTo replaces destination with the captured study.
func (s *Snapshot) CopyTo(destination string) error {
	if info, err := os.Lstat(destination); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("refusing symlinked snapshot destination: %s", destination)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(destination)
	staged, err := os.MkdirTemp(parent, ".study-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staged)
	if err := copyTree(s.dir, staged); err != nil {
		return err
	}
	backup := staged + ".old"
	hadDestination := false
	if _, err := os.Lstat(destination); err == nil {
		if err := os.Rename(destination, backup); err != nil {
			return err
		}
		hadDestination = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(staged, destination); err != nil {
		if hadDestination {
			_ = os.Rename(backup, destination)
		}
		return err
	}
	if hadDestination {
		return os.RemoveAll(backup)
	}
	return nil
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		to := filepath.Join(destination, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(to, info.Mode().Perm())
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, to)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(to, data, info.Mode().Perm())
	})
}

// Close removes the temporary copy backing the snapshot.
func (s *Snapshot) Close() error {
	if s.dir == "" {
		return nil
	}
	err := os.RemoveAll(s.dir)
	s.dir = ""
	return err
}
