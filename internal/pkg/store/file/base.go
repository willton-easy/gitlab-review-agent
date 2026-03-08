// Package file implements all store interfaces using the local filesystem.
// Each entity type is stored as individual JSON files in sub-directories.
// Thread-safety is ensured via per-directory sync.RWMutex.
package file

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// base provides common file I/O helpers with directory-level locking.
type base struct {
	dir string
	mu  sync.RWMutex
}

func newBase(rootDir, subDir string) (*base, error) {
	dir := filepath.Join(rootDir, subDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir %s: %w", dir, err)
	}
	return &base{dir: dir}, nil
}

// writeJSON atomically writes a value to a JSON file.
func (b *base) writeJSON(filename string, v any) error {
	path := filepath.Join(b.dir, filename)
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}
	// Write to temp file and rename for atomicity
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write file %s: %w", tmp, err)
	}
	return os.Rename(tmp, path)
}

// readJSON reads a JSON file into the given pointer.
// Returns os.ErrNotExist if the file does not exist.
func (b *base) readJSON(filename string, v any) error {
	path := filepath.Join(b.dir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// listFiles returns all .json filenames (not full paths) in the directory.
func (b *base) listFiles() ([]string, error) {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// exists checks if a file exists.
func (b *base) exists(filename string) bool {
	path := filepath.Join(b.dir, filename)
	_, err := os.Stat(path)
	return err == nil
}

// removeFile removes a file if it exists.
func (b *base) removeFile(filename string) error {
	path := filepath.Join(b.dir, filename)
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
