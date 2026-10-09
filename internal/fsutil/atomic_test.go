package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "out")
	if err := WriteFile(dst, strings.NewReader("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", info.Mode().Perm())
	}

	errBoom := errors.New("boom")
	rename = func(string, string) error { return errBoom }
	t.Cleanup(func() { rename = os.Rename })

	if err := WriteFile(dst, strings.NewReader("new"), 0o640); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want %v", err, errBoom)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Errorf("dst = %q after failed write, want %q", got, "old")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("leftover files: %v", entries)
	}
}
