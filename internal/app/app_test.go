package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopySymlink_CreatesLink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("content"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	src := filepath.Join(dir, "src_link")
	if err := os.Symlink(target, src); err != nil {
		t.Fatalf("create src symlink: %v", err)
	}

	dst := filepath.Join(dir, "dst_link")
	if err := copySymlink(dst, src); err != nil {
		t.Fatalf("copySymlink: %v", err)
	}

	got, err := os.Readlink(dst)
	if err != nil {
		t.Fatalf("readlink dst: %v", err)
	}
	if got != target {
		t.Errorf("symlink target: want %q got %q", target, got)
	}
}

func TestCopySymlink_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	target1 := filepath.Join(dir, "target1")
	target2 := filepath.Join(dir, "target2")
	for _, p := range []string{target1, target2} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	src := filepath.Join(dir, "src_link")
	if err := os.Symlink(target2, src); err != nil {
		t.Fatalf("create src symlink: %v", err)
	}

	dst := filepath.Join(dir, "dst_link")
	if err := os.Symlink(target1, dst); err != nil {
		t.Fatalf("create initial dst symlink: %v", err)
	}

	if err := copySymlink(dst, src); err != nil {
		t.Fatalf("copySymlink: %v", err)
	}

	got, err := os.Readlink(dst)
	if err != nil {
		t.Fatalf("readlink dst: %v", err)
	}
	if got != target2 {
		t.Errorf("symlink target after overwrite: want %q got %q", target2, got)
	}
}

func TestCopySymlink_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	src := filepath.Join(dir, "src_link")
	if err := os.Symlink(target, src); err != nil {
		t.Fatalf("create src symlink: %v", err)
	}

	dst := filepath.Join(dir, "nested", "deep", "dst_link")
	if err := copySymlink(dst, src); err != nil {
		t.Fatalf("copySymlink into nested dir: %v", err)
	}

	got, err := os.Readlink(dst)
	if err != nil {
		t.Fatalf("readlink dst: %v", err)
	}
	if got != target {
		t.Errorf("symlink target: want %q got %q", target, got)
	}
}

func TestCopySymlink_RelativeTarget(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src_link")
	if err := os.Symlink("../some/relative/path", src); err != nil {
		t.Fatalf("create src symlink: %v", err)
	}

	dst := filepath.Join(dir, "dst_link")
	if err := copySymlink(dst, src); err != nil {
		t.Fatalf("copySymlink: %v", err)
	}

	got, err := os.Readlink(dst)
	if err != nil {
		t.Fatalf("readlink dst: %v", err)
	}
	if got != "../some/relative/path" {
		t.Errorf("symlink target: want %q got %q", "../some/relative/path", got)
	}
}
