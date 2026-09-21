package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
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
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && (entry.Name() == ".git" || rel == "results" || rel == "work") {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !info.Mode().IsRegular() && info.Mode()&fs.ModeSymlink == 0 {
			return fmt.Errorf("unsupported study file %s (%s)", rel, info.Mode().Type())
		}
		return s.hashEntry(path, rel, info)
	}); err != nil {
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

func (s *Snapshot) hashEntry(source, path string, info fs.FileInfo) error {
	h := sha256.New()
	if info.Mode()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(source)
		if err != nil {
			return err
		}
		_, _ = h.Write([]byte(target))
	} else {
		file, err := os.Open(source)
		if err != nil {
			return err
		}
		if _, err := io.Copy(h, file); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	s.Files[path] = hex.EncodeToString(h.Sum(nil))
	return nil
}
