package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoDestRoot(t *testing.T) {
	repo := "/repo"
	tests := []struct {
		profile string
		want    string
	}{
		{"", repo},
		{"default", filepath.Join(repo, "profiles", "default")},
		{"laptop", filepath.Join(repo, "profiles", "laptop")},
	}
	for _, tt := range tests {
		got := repoDestRoot(repo, tt.profile)
		if got != tt.want {
			t.Errorf("repoDestRoot(%q) = %q; want %q", tt.profile, got, tt.want)
		}
	}
}

func TestResolveAddGitOps_DefaultDisabled(t *testing.T) {
	ops := resolveAddGitOps(&Config{Git: &GitAutomationConfig{}}, false, false, false)
	if ops.add || ops.commit || ops.push {
		t.Fatalf("want all false, got add=%t commit=%t push=%t", ops.add, ops.commit, ops.push)
	}
}

func TestResolveAddGitOps_CascadeFromCommit(t *testing.T) {
	ops := resolveAddGitOps(&Config{Git: &GitAutomationConfig{AutoCommit: true}}, false, false, false)
	if !ops.add || !ops.commit || ops.push {
		t.Fatalf("want add=true commit=true push=false, got add=%t commit=%t push=%t", ops.add, ops.commit, ops.push)
	}
}

func TestResolveAddGitOps_CascadeFromPush(t *testing.T) {
	ops := resolveAddGitOps(&Config{Git: &GitAutomationConfig{AutoPush: true}}, false, false, false)
	if !ops.add || !ops.commit || !ops.push {
		t.Fatalf("want all true, got add=%t commit=%t push=%t", ops.add, ops.commit, ops.push)
	}
}

func TestResolveAddGitOps_EnableFlags(t *testing.T) {
	opsAdd := resolveAddGitOps(&Config{Git: &GitAutomationConfig{}}, true, false, false)
	if !opsAdd.add || opsAdd.commit || opsAdd.push {
		t.Fatalf("want add=true commit=false push=false, got add=%t commit=%t push=%t", opsAdd.add, opsAdd.commit, opsAdd.push)
	}

	opsCommit := resolveAddGitOps(&Config{Git: &GitAutomationConfig{}}, false, true, false)
	if !opsCommit.add || !opsCommit.commit || opsCommit.push {
		t.Fatalf("want add=true commit=true push=false, got add=%t commit=%t push=%t", opsCommit.add, opsCommit.commit, opsCommit.push)
	}

	opsPush := resolveAddGitOps(&Config{Git: &GitAutomationConfig{}}, false, false, true)
	if !opsPush.add || !opsPush.commit || !opsPush.push {
		t.Fatalf("want all true, got add=%t commit=%t push=%t", opsPush.add, opsPush.commit, opsPush.push)
	}
}

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

// captureStdout redirects os.Stdout for the duration of fn and returns what
// was written to it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = orig })
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured output: %v", err)
	}
	return string(out)
}

// setupDiffFixture creates a minimal repo+config in a temp dir.
// repoFile is written to <repo>/dot_zshrc; homeFile (if non-nil) is written
// to <home>/.zshrc. Returns an *App and home/repo paths.
func setupDiffFixture(t *testing.T, repoContent []byte, homeContent []byte) (*App, string, string) {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	home := filepath.Join(dir, "home")
	cfgDir := filepath.Join(dir, "config")
	for _, d := range []string{repo, home, cfgDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "dot_zshrc"), repoContent, 0o644); err != nil {
		t.Fatalf("write dot_zshrc: %v", err)
	}
	if homeContent != nil {
		if err := os.WriteFile(filepath.Join(home, ".zshrc"), homeContent, 0o644); err != nil {
			t.Fatalf("write .zshrc: %v", err)
		}
	}
	a := &App{HomeDir: home, ConfigDir: cfgDir}
	if err := a.saveConfig(&Config{Path: repo, Profile: "default"}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	return a, home, repo
}

func TestDiff_IdenticalFiles(t *testing.T) {
	content := []byte("export PATH=$PATH:/usr/local/bin\n")
	a, _, _ := setupDiffFixture(t, content, content)

	out := captureStdout(t, func() {
		if err := a.Diff(context.Background(), "", nil); err != nil {
			t.Fatalf("Diff: %v", err)
		}
	})

	if !strings.Contains(out, "up to date") {
		t.Errorf("expected 'up to date' message, got: %q", out)
	}
}

func TestDiff_ChangedFile(t *testing.T) {
	repoContent := []byte("export PATH=$PATH:/usr/local/bin\nexport EDITOR=nvim\n")
	homeContent := []byte("export PATH=$PATH:/usr/local/bin\n")
	a, _, _ := setupDiffFixture(t, repoContent, homeContent)

	out := captureStdout(t, func() {
		if err := a.Diff(context.Background(), "", nil); err != nil {
			t.Fatalf("Diff: %v", err)
		}
	})

	if !strings.Contains(out, "@@") {
		t.Errorf("expected unified diff hunk (@@), got: %q", out)
	}
	if !strings.Contains(out, "EDITOR=nvim") {
		t.Errorf("expected added line to appear in diff, got: %q", out)
	}
	if !strings.Contains(out, "1 file(s) differ") {
		t.Errorf("expected '1 file(s) differ' summary, got: %q", out)
	}
}

func TestDiff_MissingHomeFile(t *testing.T) {
	repoContent := []byte("export EDITOR=nvim\n")
	a, _, _ := setupDiffFixture(t, repoContent, nil) // nil = no home file

	out := captureStdout(t, func() {
		if err := a.Diff(context.Background(), "", nil); err != nil {
			t.Fatalf("Diff: %v", err)
		}
	})

	if !strings.Contains(out, "@@") {
		t.Errorf("expected unified diff hunk (@@), got: %q", out)
	}
	if !strings.Contains(out, "+export EDITOR=nvim") {
		t.Errorf("expected all-additions diff, got: %q", out)
	}
}

func TestDiff_BinaryFile(t *testing.T) {
	binaryContent := []byte("header\x00binary\x00data")
	a, _, _ := setupDiffFixture(t, binaryContent, []byte("other"))

	out := captureStdout(t, func() {
		if err := a.Diff(context.Background(), "", nil); err != nil {
			t.Fatalf("Diff: %v", err)
		}
	})

	if !strings.Contains(out, "Binary files") || !strings.Contains(out, "differ") {
		t.Errorf("expected 'Binary files ... differ' message, got: %q", out)
	}
	if strings.Contains(out, "@@") {
		t.Errorf("unexpected unified diff hunk for binary file, got: %q", out)
	}
}

func TestDiff_FilterByFilename(t *testing.T) {
	repoContent := []byte("new line\n")
	homeContent := []byte("old line\n")
	a, _, _ := setupDiffFixture(t, repoContent, homeContent)

	out := captureStdout(t, func() {
		if err := a.Diff(context.Background(), "", []string{".zshrc"}); err != nil {
			t.Fatalf("Diff: %v", err)
		}
	})

	if !strings.Contains(out, "@@") {
		t.Errorf("expected unified diff hunk (@@), got: %q", out)
	}
}

func TestSync_UpdatesRepoFromHome(t *testing.T) {
	repoContent := []byte("old line\n")
	homeContent := []byte("new line\n")
	a, _, repo := setupDiffFixture(t, repoContent, homeContent)

	if err := a.Sync(context.Background(), "", false, false, false, false); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(repo, "dot_zshrc"))
	if err != nil {
		t.Fatalf("read dot_zshrc: %v", err)
	}
	if !bytes.Equal(got, homeContent) {
		t.Errorf("repo file not updated: got %q, want %q", got, homeContent)
	}
}

func TestSync_NoChange(t *testing.T) {
	content := []byte("same line\n")
	a, _, repo := setupDiffFixture(t, content, content)

	if err := a.Sync(context.Background(), "", false, false, false, false); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(repo, "dot_zshrc"))
	if err != nil {
		t.Fatalf("read dot_zshrc: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("repo file changed unexpectedly: got %q, want %q", got, content)
	}
}

func TestSync_DryRunDoesNotWrite(t *testing.T) {
	repoContent := []byte("old line\n")
	homeContent := []byte("new line\n")
	a, _, repo := setupDiffFixture(t, repoContent, homeContent)

	if err := a.Sync(context.Background(), "", true, false, false, false); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(repo, "dot_zshrc"))
	if err != nil {
		t.Fatalf("read dot_zshrc: %v", err)
	}
	if !bytes.Equal(got, repoContent) {
		t.Errorf("dry-run modified repo file: got %q, want %q", got, repoContent)
	}
}

func TestSync_SkipsMissingHomeFile(t *testing.T) {
	repoContent := []byte("old line\n")
	a, _, repo := setupDiffFixture(t, repoContent, nil)

	if err := a.Sync(context.Background(), "", false, false, false, false); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(repo, "dot_zshrc"))
	if err != nil {
		t.Fatalf("read dot_zshrc: %v", err)
	}
	if !bytes.Equal(got, repoContent) {
		t.Errorf("repo file changed despite missing home file: got %q, want %q", got, repoContent)
	}
}

