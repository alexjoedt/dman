package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveReadConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	a := &App{ConfigDir: dir}

	cfg := &Config{
		RepositoryURL: "https://github.com/user/dotfiles.git",
		Profile:       "work",
		Path:          filepath.Join(dir, "repo"),
		Git: &GitAutomationConfig{
			AutoAdd:    true,
			AutoCommit: true,
			AutoPush:   true,
		},
	}

	if err := a.saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	got, err := a.readConfig()
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
	if got.Git == nil {
		t.Fatal("Git: expected non-nil")
	}
	if got.Git.AutoAdd != cfg.Git.AutoAdd {
		t.Errorf("Git.AutoAdd: want %t got %t", cfg.Git.AutoAdd, got.Git.AutoAdd)
	}
	if got.Git.AutoCommit != cfg.Git.AutoCommit {
		t.Errorf("Git.AutoCommit: want %t got %t", cfg.Git.AutoCommit, got.Git.AutoCommit)
	}
	if got.Git.AutoPush != cfg.Git.AutoPush {
		t.Errorf("Git.AutoPush: want %t got %t", cfg.Git.AutoPush, got.Git.AutoPush)
	}
}

func TestReadConfig_DefaultGitAutomationDisabled(t *testing.T) {
	dir := t.TempDir()
	a := &App{ConfigDir: dir}

	cfg := &Config{
		RepositoryURL: "https://github.com/user/dotfiles.git",
		Profile:       "default",
		Path:          filepath.Join(dir, "repo"),
	}

	if err := a.saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	got, err := a.readConfig()
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if got.Git == nil {
		t.Fatal("Git: expected non-nil")
	}
	if got.Git.AutoAdd {
		t.Error("Git.AutoAdd: want false got true")
	}
	if got.Git.AutoCommit {
		t.Error("Git.AutoCommit: want false got true")
	}
	if got.Git.AutoPush {
		t.Error("Git.AutoPush: want false got true")
	}
}

func TestReadConfig_GitAutomationCascade(t *testing.T) {
	dir := t.TempDir()
	a := &App{ConfigDir: dir}

	if err := a.saveConfig(&Config{
		RepositoryURL: "https://github.com/user/dotfiles.git",
		Profile:       "default",
		Path:          filepath.Join(dir, "repo"),
		Git: &GitAutomationConfig{
			AutoCommit: true,
		},
	}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	gotCommit, err := a.readConfig()
	if err != nil {
		t.Fatalf("readConfig with autoCommit: %v", err)
	}
	if !gotCommit.Git.AutoAdd {
		t.Error("Git.AutoAdd: want true when AutoCommit is true")
	}

	if err := a.saveConfig(&Config{
		RepositoryURL: "https://github.com/user/dotfiles.git",
		Profile:       "default",
		Path:          filepath.Join(dir, "repo"),
		Git: &GitAutomationConfig{
			AutoPush: true,
		},
	}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	gotPush, err := a.readConfig()
	if err != nil {
		t.Fatalf("readConfig with autoPush: %v", err)
	}
	if !gotPush.Git.AutoAdd {
		t.Error("Git.AutoAdd: want true when AutoPush is true")
	}
	if !gotPush.Git.AutoCommit {
		t.Error("Git.AutoCommit: want true when AutoPush is true")
	}
}

func TestReadConfig_ErrNoConfig(t *testing.T) {
	a := &App{ConfigDir: t.TempDir()}

	_, err := a.readConfig()
	if !errors.Is(err, ErrNoConfig) {
		t.Errorf("expected ErrNoConfig, got %v", err)
	}
}

func TestSaveConfig_CreatesJSON(t *testing.T) {
	dir := t.TempDir()
	a := &App{ConfigDir: dir}

	cfg := &Config{
		RepositoryURL: "https://github.com/user/dotfiles.git",
		Profile:       "default",
		Path:          "/home/user/.local/share/dman",
	}

	if err := a.saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	jsonPath := filepath.Join(dir, configFileName)
	if _, err := os.Stat(jsonPath); err != nil {
		t.Errorf("expected %s to exist: %v", jsonPath, err)
	}
}

func TestErrNoConfig(t *testing.T) {
	if !errors.Is(ErrNoConfig, ErrNoConfig) {
		t.Error("ErrNoConfig sentinel not comparable with errors.Is")
	}
}
