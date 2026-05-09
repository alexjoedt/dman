package dman

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// App holds all resolved application paths.
type App struct {
	HomeDir   string
	HomeMode  os.FileMode
	ConfigDir string // ~/.config/dman
	BackupDir string // ~/.local/state/dman/backups
}

type filePair struct {
	src string
	dst string
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

	backupDir := filepath.Join(h, ".local", "state", "dman", "backups")
	if !isExist(backupDir) {
		if err := os.MkdirAll(backupDir, info.Mode()); err != nil {
			return nil, fmt.Errorf("create backup dir %s: %w", backupDir, err)
		}
	}

	return &App{
		HomeDir:   h,
		HomeMode:  info.Mode(),
		ConfigDir: configDir,
		BackupDir: backupDir,
	}, nil
}

func isExist(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// copyFile copies src to dst, preserving the source file's permissions.
func copyFile(dst, src string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// transformPath transforms a home-relative dotfile path to a repo path.
// ~/.zshrc -> <repo>/dot_zshrc
// ~/.config/nvim/init.lua -> <repo>/dot_config/nvim/init.lua
func transformPath(home, repo string, p string) (string, error) {
	p = strings.TrimPrefix(p, home+string(filepath.Separator))
	if len(p) == 0 || p[0] != '.' {
		return "", fmt.Errorf("not a dotfile: %s", p)
	}
	p = strings.Replace(p, ".", "dot_", 1)
	return filepath.Join(repo, p), nil
}
