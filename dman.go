package dman

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

var (
	homeDir  *FileInfo
	homeOnce sync.Once

	repoDir             string
	repoDestinationOnce sync.Once

	configDir             string
	configDestinationOnce sync.Once
	configFile            string
	configFileOnce        sync.Once
	databasePath          string
	databasePathOnce      sync.Once
)

type FileInfo struct {
	os.FileInfo
	Path string
}

// Name returns the fullpath
func (f *FileInfo) Name() string {
	return f.Path
}

func HomeDir() *FileInfo {
	homeOnce.Do(func() {
		h, err := os.UserHomeDir()
		if err != nil {
			panic(fmt.Sprintf("failed to get home directory: %v", err))
		}

		info, err := os.Stat(h)
		if err != nil {
			panic(fmt.Sprintf("failed to get home stat: %v", err))
		}
		homeDir = &FileInfo{
			FileInfo: info,
			Path:     h,
		}
	})

	return homeDir
}

func RepoDir() string {
	repoDestinationOnce.Do(func() {

		share := filepath.Join(HomeDir().Name(), ".local", "share")
		if !isExist(share) {
			if err := os.MkdirAll(share, HomeDir().Mode()); err != nil {
				panic(fmt.Sprintf("failed to create '%s': %v", share, err))
			}
		}

		repoDir = filepath.Join(share, "dman")
	})

	return repoDir
}

func ConfigDir() string {
	configDestinationOnce.Do(func() {

		c := filepath.Join(HomeDir().Name(), ".config", "dman")
		if !isExist(c) {
			if err := os.MkdirAll(c, HomeDir().Mode()); err != nil {
				panic(fmt.Sprintf("failed to create '%s': %v", c, err))
			}
		}

		configDir = c
	})

	return configDir
}

func ConfigFile() string {
	configFileOnce.Do(func() {
		c := filepath.Join(ConfigDir(), "config")
		configFile = c
	})
	return configFile
}

func DatabasePath() string {
	databasePathOnce.Do(func() {
		p := filepath.Join(ConfigDir(), "dman.db")
		databasePath = p
	})

	return databasePath
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
