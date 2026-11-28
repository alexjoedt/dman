package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPurge(t *testing.T) {
	outside := t.TempDir()
	tests := []struct {
		name       string
		answer     string
		repo       func(home string) string // repo path, relative to a fresh home
		dotfiles   bool
		wantConfig bool
		wantRepo   bool
		wantWarn   bool
	}{
		{"answer no keeps everything", "n\n", func(h string) string { return filepath.Join(h, "dotfiles") }, true, true, true, false},
		{"empty answer keeps everything", "\n", func(h string) string { return filepath.Join(h, "dotfiles") }, true, true, true, false},
		{"yes removes repo inside home", "y\n", func(h string) string { return filepath.Join(h, "dotfiles") }, true, false, false, false},
		{"yes keeps repo outside home", "y\n", func(string) string { return filepath.Join(outside, "elsewhere") }, true, false, true, true},
		{"yes keeps inside-home dir that is no dman repo", "y\n", func(h string) string { return filepath.Join(h, "dotfiles") }, false, false, true, true},
		{"yes removes repo at default clone path", "y\n", func(string) string { return filepath.Join(outside, "dman") }, true, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("XDG_DATA_HOME", outside)
			repo := tt.repo(home)
			if tt.dotfiles {
				touch(t, filepath.Join(repo, "dot_bashrc"))
			} else {
				touch(t, filepath.Join(repo, "notes.txt"))
			}
			a := &App{HomeDir: home, HomeMode: 0o755, ConfigDir: filepath.Join(home, ".config", "dman")}
			if err := a.saveConfig(&Config{Path: repo, Profile: "default"}); err != nil {
				t.Fatalf("saveConfig: %v", err)
			}
			logs := captureLog(t)
			stdin(t, tt.answer)

			if err := a.Purge(context.Background()); err != nil {
				t.Fatalf("Purge: %v", err)
			}
			if got := isExist(filepath.Join(a.ConfigDir, configFileName)); got != tt.wantConfig {
				t.Errorf("config exists = %v, want %v", got, tt.wantConfig)
			}
			if got := isExist(repo); got != tt.wantRepo {
				t.Errorf("repo exists = %v, want %v", got, tt.wantRepo)
			}
			if got := strings.Contains(logs.String(), "the repository is kept"); got != tt.wantWarn {
				t.Errorf("keep warning = %v, want %v\n%s", got, tt.wantWarn, logs)
			}
		})
	}
}

func stdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = prev; _ = r.Close() })
}
