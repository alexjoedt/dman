package dman

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexjoedt/blobfs"
)

// App holds all resolved application paths.
type App struct {
	HomeDir   string
	HomeMode  os.FileMode
	RepoDir   string
	ConfigDir string
	DBPath    string
	Blobs     *blobfs.Storage
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

	a := &App{
		HomeDir:   h,
		HomeMode:  info.Mode(),
		ConfigDir: configDir,
		DBPath:    filepath.Join(configDir, "dman.db"),
	}

	blobsDir := filepath.Join(configDir, "objects")
	a.Blobs, err = blobfs.NewStorage(blobsDir,
		blobfs.WithFileMode(info.Mode().Perm()),
		blobfs.WithDirMode(info.Mode()),
	)
	if err != nil {
		return nil, fmt.Errorf("init object store: %w", err)
	}

	if config, err := a.readConfig(); err == nil {
		a.RepoDir = config.Path
	} else {
		share := filepath.Join(h, ".local", "share")
		if !isExist(share) {
			if err := os.MkdirAll(share, info.Mode()); err != nil {
				return nil, fmt.Errorf("create share dir %s: %w", share, err)
			}
		}
		a.RepoDir = filepath.Join(share, "dman")
	}

	return a, nil
}

func isExist(p string) bool {
	_, err := os.Stat(p)
	return !os.IsNotExist(err)
}

func copyFile(dst, src string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return err
	}

	return nil
}

// transformPath transforms a path with home as base to a repo path.
// It replaces all leading dots inside home with 'dot_'
// <home>/.zshrc --> <path-to-repo>/dot_zshrc
func transformPath(home, repo string, p string) (string, error) {
	p = strings.TrimPrefix(p, home+string(filepath.Separator))
	if p[0] != '.' {
		return "", fmt.Errorf("not a dotfile: %s", p)
	}
	p = strings.Replace(p, ".", "dot_", 1)
	return filepath.Join(repo, p), nil
}

func validateShortID(id string) error {
	if len(id) < 12 {
		return errors.New("id is too short")
	}
	return nil
}
