package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Snapshot contains the files and hash that identify a version of a study.
type Snapshot struct {
	Files map[string]string
	Hash  string
}

// Capture hashes a study before its setup command can change it.
func Capture(root string) (*Snapshot, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	s := &Snapshot{Files: map[string]string{}}
	if err := s.walk(root, ""); err != nil {
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

func (s *Snapshot) walk(root, rel string) error {
	abs := filepath.Join(root, filepath.FromSlash(rel))
	entries, err := os.ReadDir(abs)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := entry.Name()
		if rel != "" {
			path = rel + "/" + path
		}
		if entry.IsDir() && (entry.Name() == ".git" || rel == "" && (entry.Name() == "results" || entry.Name() == "work")) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := s.walk(root, path); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() && info.Mode()&fs.ModeSymlink == 0 {
			return fmt.Errorf("unsupported study file %s (%s)", path, info.Mode().Type())
		}
		if err := s.hashEntry(root, path, info); err != nil {
			return err
		}
	}
	return nil
}

func (s *Snapshot) hashEntry(root, path string, info fs.FileInfo) error {
	source := filepath.Join(root, filepath.FromSlash(path))
	var content []byte
	var err error
	if info.Mode()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(source)
		if err != nil {
			return err
		}
		content = []byte(target)
	} else {
		content, err = os.ReadFile(source)
		if err != nil {
			return err
		}
	}
	h := sha256.Sum256(content)
	s.Files[path] = hex.EncodeToString(h[:])
	return nil
}
