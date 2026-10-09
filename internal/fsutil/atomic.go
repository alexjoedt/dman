// Package fsutil holds small filesystem helpers shared across dman packages.
package fsutil

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// rename is a test seam for the final step of the atomic write.
var rename = os.Rename

// WriteFile writes r to dst with the given mode. The content goes to a
// temporary file in dst's directory first, is synced and renamed into place,
// so a crash or a failed write never leaves a truncated destination. The
// parent directory must exist.
func WriteFile(dst string, r io.Reader, mode fs.FileMode) error {
	return write(dst, r, mode.Perm(), true)
}

// WriteJSON writes v as indented JSON to dst, atomically like WriteFile. A
// symlink at dst is followed so its target is updated, and an existing file
// keeps its permissions; a new file gets 0600 minus the umask.
func WriteJSON(dst string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	// A dangling symlink fails to resolve and is replaced by a regular file.
	if p, err := filepath.EvalSymlinks(dst); err == nil {
		dst = p
	}
	info, err := os.Stat(dst)
	var perm fs.FileMode
	if err == nil {
		perm = info.Mode().Perm()
	}
	return write(dst, bytes.NewReader(append(data, '\n')), perm, err == nil)
}

func write(dst string, r io.Reader, perm fs.FileMode, chmod bool) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".dman-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()

	if _, err = io.Copy(tmp, r); err != nil {
		return err
	}
	// Apply the mode before the rename so the file never appears at dst with
	// the wrong permissions.
	if chmod {
		if err = tmp.Chmod(perm); err != nil {
			return err
		}
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return rename(tmp.Name(), dst)
}
