package dman

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testManifestTOML = `
[packages]
brew   = ["ripgrep", "fd", "fzf"]
apt    = ["ripgrep", "fd-find"]
pacman = ["ripgrep", "fd", "fzf", "starship"]

[dirs]
paths = ["~/dev", "~/projects"]

[[repos]]
url  = "git@github.com:alexjoedt/dotfiles.git"
dest = "~/dev/dotfiles"

[[repos]]
url  = "git@github.com:alexjoedt/dman.git"
dest = "~/dev/dman"
`

func TestReadManifest_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.toml")
	require.NoError(t, os.WriteFile(path, []byte(testManifestTOML), 0o644))

	m, err := readManifest(path)
	require.NoError(t, err)
	require.NotNil(t, m)

	assert.Equal(t, []string{"ripgrep", "fd", "fzf"}, m.Packages.Brew)
	assert.Equal(t, []string{"ripgrep", "fd-find"}, m.Packages.Apt)
	assert.Equal(t, []string{"ripgrep", "fd", "fzf", "starship"}, m.Packages.Pacman)
	assert.Equal(t, []string{"~/dev", "~/projects"}, m.Dirs.Paths)
	require.Len(t, m.Repos, 2)
	assert.Equal(t, "git@github.com:alexjoedt/dotfiles.git", m.Repos[0].URL)
	assert.Equal(t, "~/dev/dotfiles", m.Repos[0].Dest)
	assert.Equal(t, "git@github.com:alexjoedt/dman.git", m.Repos[1].URL)
	assert.Equal(t, "~/dev/dman", m.Repos[1].Dest)
}

func TestReadManifest_Missing(t *testing.T) {
	m, err := readManifest("/nonexistent/path/manifest.toml")
	require.NoError(t, err)
	assert.Nil(t, m)
}

func TestExpandHome(t *testing.T) {
	home := "/home/user"
	tests := []struct {
		input string
		want  string
	}{
		{"~/dev", "/home/user/dev"},
		{"~/dev/personal/project", "/home/user/dev/personal/project"},
		{"~", "/home/user"},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, expandHome(tt.input, home))
		})
	}
}

func TestPackagesFor(t *testing.T) {
	p := Packages{
		Brew:   []string{"ripgrep", "fd"},
		Apt:    []string{"ripgrep", "fd-find"},
		Pacman: []string{"ripgrep", "fd", "starship"},
	}
	assert.Equal(t, p.Brew, p.packagesFor("brew"))
	assert.Equal(t, p.Apt, p.packagesFor("apt-get"))
	assert.Equal(t, p.Pacman, p.packagesFor("pacman"))
	assert.Equal(t, p.Pacman, p.packagesFor("yay"))
	assert.Equal(t, p.Pacman, p.packagesFor("paru"))
	assert.Nil(t, p.packagesFor("unknown"))
}

func TestManagerArgs(t *testing.T) {
	pkgs := []string{"ripgrep", "fd"}
	tests := []struct {
		manager string
		want    []string
	}{
		{"brew", []string{"install", "ripgrep", "fd"}},
		{"apt-get", []string{"install", "-y", "ripgrep", "fd"}},
		{"pacman", []string{"-S", "--needed", "--noconfirm", "ripgrep", "fd"}},
		{"yay", []string{"-S", "--needed", "--noconfirm", "ripgrep", "fd"}},
		{"paru", []string{"-S", "--needed", "--noconfirm", "ripgrep", "fd"}},
	}
	for _, tt := range tests {
		t.Run(tt.manager, func(t *testing.T) {
			assert.Equal(t, tt.want, managerArgs(tt.manager, pkgs))
		})
	}
}
