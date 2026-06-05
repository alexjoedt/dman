package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// App holds all resolved application paths.
type App struct {
	HomeDir     string
	HomeMode    os.FileMode
	ConfigDir   string // ~/.config/dman
	SnapshotDir string // set only when snapshots.enabled is true
}

// NewApp resolves all paths from the environment and returns a ready-to-use App.
// It creates required directories if they do not exist.
func NewApp() (*App, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}

	info, err := os.Stat(h)
	if err != nil {
		return nil, fmt.Errorf("stat home directory: %w", err)
	}

	configDir := filepath.Join(h, ".config", "dman")
	if !isExist(configDir) {
		if err := os.MkdirAll(configDir, info.Mode()); err != nil {
			return nil, fmt.Errorf("create config dir %s: %w", configDir, err)
		}
	}

	return &App{
		HomeDir:   h,
		HomeMode:  info.Mode(),
		ConfigDir: configDir,
	}, nil
}

func isExist(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// copyFile copies src to dst, preserving the source file's permissions.
func copyFile(dst, src string) (err error) {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = srcFile.Close() }()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer func() {
		if cerr := dstFile.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// isBinaryFile detects binary content by scanning the first chunk for NUL bytes.
// This keeps detection dependency-free and fast for common script directories.
func isBinaryFile(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	buf := make([]byte, 8192)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return false, err
	}

	return bytes.Contains(buf[:n], []byte{0}), nil
}
