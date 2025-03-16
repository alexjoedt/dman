package dman

import (
	"context"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type DotfileManager struct {
	Repo    string
	Home    string
	Force   bool
	Verbose bool

	basePath string
	// db       *DB
}

func (dm *DotfileManager) Apply(ctx context.Context) error {
	if _, err := exec.LookPath("git"); err != nil {
		return &Err{err: "git is not installed", cause: err, fatal: true}
	}

	share := RepoDir()
	dm.basePath = filepath.Join(share, "flow")

	if _, err := url.Parse(dm.Repo); err != nil {
		return &Err{err: "failed to parse URL", cause: err, fatal: true}
	}

	var cmd *exec.Cmd
	if isExist(dm.basePath) && isExist(filepath.Join(dm.basePath, ".git")) {
		cmd = exec.CommandContext(ctx, "git", "-C", dm.basePath, "pull")
	} else {
		cmd = exec.CommandContext(ctx, "git", "clone", dm.Repo, dm.basePath)
	}
	err := cmd.Run()
	if err != nil {
		return &Err{err: "command ends with an error", cause: err, fatal: true}
	}

	err = dm.updateFiles()
	if err != nil {
		return &Err{err: "failed to update files", cause: err, fatal: true}
	}
	return nil
}

func (dm *DotfileManager) updateFiles() error {
	entries, err := os.ReadDir(dm.basePath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if strings.Contains(entry.Name(), "dot_") {
			if err := dm.update(dm.basePath, entry); err != nil {
				continue
			}
		}
	}

	return nil
}

func (dm *DotfileManager) update(p string, entry os.DirEntry) error {
	if entry.IsDir() {
		entries, err := os.ReadDir(filepath.Join(p, entry.Name()))
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := dm.update(filepath.Join(p, entry.Name()), e); err != nil {
				return err
			}
		}
		return nil
	}

	src := filepath.Join(p, entry.Name())
	dst := strings.ReplaceAll(src, dm.basePath, dm.Home)
	dst = strings.ReplaceAll(dst, "dot_", ".")

	if isExist(dst) && !dm.Force {
		return nil
	}

	// TODO: check hash value from db
	// TODO: save file content in db

	if err := copyFile(dst, src); err != nil {
		return err
	}

	return nil
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
