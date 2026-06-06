package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCd_StartsShellInRepositoryPath(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo path: %v", err)
	}

	a := &App{ConfigDir: dir}
	if err := a.saveConfig(&Config{Path: repoPath}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	t.Setenv("SHELL", "/bin/sh")

	called := false
	originalRunShell := runShell
	runShell = func(ctx context.Context, shell, dir string) error {
		called = true
		if shell != "/bin/sh" {
			t.Fatalf("unexpected shell: %q", shell)
		}
		if dir != repoPath {
			t.Fatalf("unexpected directory: want %q got %q", repoPath, dir)
		}
		return nil
	}
	t.Cleanup(func() {
		runShell = originalRunShell
	})

	if err := a.Cd(context.Background()); err != nil {
		t.Fatalf("Cd: %v", err)
	}
	if !called {
		t.Fatal("expected runShell to be called")
	}
}

func TestCd_MissingShell(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo path: %v", err)
	}

	a := &App{ConfigDir: dir}
	if err := a.saveConfig(&Config{Path: repoPath}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	t.Setenv("SHELL", "")

	err := a.Cd(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "SHELL is not set" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCd_MissingRepositoryPath(t *testing.T) {
	dir := t.TempDir()
	a := &App{ConfigDir: dir}

	missingPath := filepath.Join(dir, "missing")
	if err := a.saveConfig(&Config{Path: missingPath}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	err := a.Cd(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "repository path does not exist: "+missingPath {
		t.Fatalf("unexpected error: %v", err)
	}
}
