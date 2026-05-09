package dman

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Manifest represents the parsed manifest.toml from the dotfiles repository.
type Manifest struct {
	Packages Packages `toml:"packages"`
	Dirs     Dirs     `toml:"dirs"`
	Repos    []Repo   `toml:"repos"`
}

// Packages holds per-manager package lists.
type Packages struct {
	Brew   []string `toml:"brew"`
	Apt    []string `toml:"apt"`
	Pacman []string `toml:"pacman"`
}

// Dirs holds directory paths to create.
type Dirs struct {
	Paths []string `toml:"paths"`
}

// Repo describes a git repository to clone.
type Repo struct {
	URL  string `toml:"url"`
	Dest string `toml:"dest"`
}

// readManifest decodes a manifest.toml file. Returns nil, nil if the file does not exist.
func readManifest(path string) (*Manifest, error) {
	if !isExist(path) {
		return nil, nil
	}
	var m Manifest
	if _, err := toml.DecodeFile(path, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// detectManager returns the first available package manager found in PATH.
// Preference order: brew → yay → paru → pacman → apt-get.
func detectManager() string {
	for _, m := range []string{"brew", "yay", "paru", "pacman", "apt-get"} {
		if _, err := exec.LookPath(m); err == nil {
			return m
		}
	}
	return ""
}

// expandHome replaces a leading ~/ with the given home directory.
func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

// isDirEmpty reports whether the directory at path is empty (or does not exist).
func isDirEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return true
	}
	return len(entries) == 0
}
