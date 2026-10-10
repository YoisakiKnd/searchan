package fsutil

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func WriteJSON(path string, value any) error {
	return WriteJSONLimit(path, value, 0)
}

var ErrSizeLimit = errors.New("JSON file exceeds size limit")

type limitedWriter struct {
	next      io.Writer
	remaining int64
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, ErrSizeLimit
	}
	n, err := w.next.Write(p)
	w.remaining -= int64(n)
	return n, err
}

// WriteJSONLimit preserves the previous file when encoding exceeds the limit.
// Zero keeps WriteJSON's existing behavior for trusted local artifacts.
func WriteJSONLimit(path string, value any, limit int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".json-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	var writer io.Writer = f
	if limit > 0 {
		writer = &limitedWriter{next: f, remaining: limit}
	}
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err == nil {
		err = dir.Sync()
		dir.Close()
	}
	return err
}

// WalkDirs uses bounded batches rather than loading the full directory listing.
func WalkDirs(root string, fn func(string) error) error {
	f, err := os.Open(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		entries, err := f.ReadDir(64)
		for _, entry := range entries {
			if entry.IsDir() {
				if callErr := fn(entry.Name()); callErr != nil {
					return callErr
				}
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
