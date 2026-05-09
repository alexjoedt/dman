package dman

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveReadConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	app := &App{ConfigDir: dir}

	cfg := &Config{
		RepositoryURL: "https://github.com/user/dotfiles.git",
		Profile:       "work",
		Path:          filepath.Join(dir, "repo"),
	}

	if err := app.saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	got, err := app.readConfig()
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}

	if got.RepositoryURL != cfg.RepositoryURL {
		t.Errorf("RepositoryURL: want %q got %q", cfg.RepositoryURL, got.RepositoryURL)
	}
	if got.Profile != cfg.Profile {
		t.Errorf("Profile: want %q got %q", cfg.Profile, got.Profile)
	}
	if got.Path != cfg.Path {
		t.Errorf("Path: want %q got %q", cfg.Path, got.Path)
	}
}

func TestReadConfig_ErrNoConfig(t *testing.T) {
	app := &App{ConfigDir: t.TempDir()}

	_, err := app.readConfig()
	if !errors.Is(err, ErrNoConfig) {
		t.Errorf("expected ErrNoConfig, got %v", err)
	}
}

func TestSaveConfig_CreatesJSON(t *testing.T) {
	dir := t.TempDir()
	app := &App{ConfigDir: dir}

	cfg := &Config{
		RepositoryURL: "https://github.com/user/dotfiles.git",
		Profile:       "default",
		Path:          "/home/user/.local/share/dman",
	}

	if err := app.saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	jsonPath := filepath.Join(dir, configFileName)
	if _, err := os.Stat(jsonPath); err != nil {
		t.Errorf("expected %s to exist: %v", jsonPath, err)
	}
}
