// Package fsutil holds small filesystem helpers shared across dman packages.
package fsutil

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// rename is a test seam for the final step of WriteFile.
var rename = os.Rename

// WriteFile writes r to dst with the given mode. The content goes to a
// temporary file in dst's directory first and is renamed into place, so a
// crash or a failed write never leaves a truncated destination. The parent
// directory must exist.
func WriteFile(dst string, r io.Reader, mode fs.FileMode) (err error) {
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
	// CreateTemp always uses 0600; apply the intended mode before the rename
	// so the file never appears at dst with the wrong permissions.
	if err = tmp.Chmod(mode.Perm()); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return rename(tmp.Name(), dst)
}
