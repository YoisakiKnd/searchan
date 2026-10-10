package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOversizedJSONCannotReplaceDurableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")
	if err := WriteJSONLimit(path, map[string]string{"value": "old"}, 100); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteJSONLimit(path, map[string]string{"value": strings.Repeat("<", 100)}, 100); !errors.Is(err, ErrSizeLimit) {
		t.Fatal("escaped JSON exceeded limit without rejection", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("oversized encode replaced previous durable record")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary file leaked")
	}
}
