package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gitRemoteFixture is a cryptEnv whose repo is a clone of a bare remote on
// branch main with one seeded commit. Git config is isolated from the user's.
type gitRemoteFixture struct {
	*cryptEnv
	remote string
}

func newGitRemoteFixture(t *testing.T, seed map[string]string, git GitAutomationConfig) *gitRemoteFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH, skipping git tail test")
	}
	e := setupCryptFixture(t, true)
	gitconfig := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gitconfig, []byte("[user]\n\tname = t\n\temail = t@t\n[init]\n\tdefaultBranch = main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}

	remote := filepath.Join(filepath.Dir(e.repo), "remote.git")
	gitRun(t, "init", "-q", "--bare", remote)
	gitRun(t, "init", "-q", e.repo)
	gitRun(t, "-C", e.repo, "remote", "add", "origin", remote)
	for rel, content := range seed {
		e.writeRepo(t, rel, content)
	}
	gitRun(t, "-C", e.repo, "commit", "-q", "--allow-empty", "-m", "init")
	if len(seed) > 0 {
		gitRun(t, "-C", e.repo, "add", ".")
		gitRun(t, "-C", e.repo, "commit", "-q", "-m", "seed")
	}
	gitRun(t, "-C", e.repo, "push", "-q", "-u", "origin", "main")

	e.cfg.Git = &git
	if err := e.app.saveConfig(e.cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	return &gitRemoteFixture{cryptEnv: e, remote: remote}
}

func gitRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *gitRemoteFixture) remoteFiles(t *testing.T) []string {
	t.Helper()
	return strings.Split(gitRun(t, "--git-dir", f.remote, "ls-tree", "-r", "--name-only", "main"), "\n")
}

func (f *gitRemoteFixture) remoteSubject(t *testing.T) string {
	t.Helper()
	return gitRun(t, "--git-dir", f.remote, "log", "-1", "--format=%s", "main")
}

func TestAdd_PushesNewFileToRemote(t *testing.T) {
	f := newGitRemoteFixture(t, nil, GitAutomationConfig{AutoPush: true})
	src := f.writeHome(t, ".zshrc", "export A=1\n")

	if err := f.app.Add(context.Background(), []string{src}, "", false, false, false, false, false); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := f.remoteFiles(t); !slices.Contains(got, "dot_zshrc") {
		t.Errorf("remote files = %q, want dot_zshrc", got)
	}
	if got := f.remoteSubject(t); got != "add .zshrc" {
		t.Errorf("remote subject = %q, want %q", got, "add .zshrc")
	}
	if got := gitRun(t, "--git-dir", f.remote, "show", "main:dot_zshrc"); got != "export A=1" {
		t.Errorf("remote dot_zshrc = %q", got)
	}
}

func TestSync_PushesChangedFileToRemote(t *testing.T) {
	f := newGitRemoteFixture(t, map[string]string{"dot_zshrc": "old\n", "dot_vimrc": "same\n"}, GitAutomationConfig{AutoPush: true})
	f.writeHome(t, ".zshrc", "new\n")
	f.writeHome(t, ".vimrc", "same\n")

	if err := f.app.Sync(context.Background(), "", false, false, false, false, nil); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := f.remoteSubject(t); got != "sync tracked files (~1)" {
		t.Errorf("remote subject = %q", got)
	}
	if got := gitRun(t, "--git-dir", f.remote, "show", "main:dot_zshrc"); got != "new" {
		t.Errorf("remote dot_zshrc = %q, want new", got)
	}
	if got := gitRun(t, "--git-dir", f.remote, "show", "--name-only", "--format=", "main"); got != "dot_zshrc" {
		t.Errorf("files in sync commit = %q, want dot_zshrc", got)
	}
}

func TestSync_NothingChangedCreatesNoCommit(t *testing.T) {
	f := newGitRemoteFixture(t, map[string]string{"dot_zshrc": "same\n"}, GitAutomationConfig{AutoCommit: true})
	f.writeHome(t, ".zshrc", "same\n")
	head := gitRun(t, "-C", f.repo, "rev-parse", "HEAD")

	if err := f.app.Sync(context.Background(), "", false, false, false, false, nil); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := gitRun(t, "-C", f.repo, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved from %s to %s", head, got)
	}
}

func TestSync_AutoCommitCommitsLocallyWithoutPush(t *testing.T) {
	f := newGitRemoteFixture(t, map[string]string{"dot_zshrc": "old\n"}, GitAutomationConfig{AutoCommit: true})
	f.writeHome(t, ".zshrc", "new\n")
	pushed := f.remoteSubject(t)

	if err := f.app.Sync(context.Background(), "", false, false, false, false, nil); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := gitRun(t, "-C", f.repo, "log", "-1", "--format=%s"); got != "sync tracked files (~1)" {
		t.Errorf("local subject = %q", got)
	}
	if got := f.remoteSubject(t); got != pushed {
		t.Errorf("remote subject = %q, want unchanged %q", got, pushed)
	}
}

func TestSync_AutoAddStagesWithoutCommit(t *testing.T) {
	f := newGitRemoteFixture(t, map[string]string{"dot_zshrc": "old\n"}, GitAutomationConfig{AutoAdd: true})
	f.writeHome(t, ".zshrc", "new\n")
	head := gitRun(t, "-C", f.repo, "rev-parse", "HEAD")

	if err := f.app.Sync(context.Background(), "", false, false, false, false, nil); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := gitRun(t, "-C", f.repo, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved from %s to %s", head, got)
	}
	if got := gitRun(t, "-C", f.repo, "status", "--porcelain"); got != "M  dot_zshrc" {
		t.Errorf("git status = %q, want staged dot_zshrc", got)
	}
}

func TestSave_PushesOnlyTargetToRemote(t *testing.T) {
	f := newGitRemoteFixture(t, map[string]string{"dot_config/app/conf": "old\n", "dot_zshrc": "old\n"}, GitAutomationConfig{AutoPush: true})
	f.writeHome(t, ".config/app/conf", "new\n")
	f.writeHome(t, ".zshrc", "new\n")

	if err := f.app.Sync(context.Background(), "", false, false, false, false, []string{"~/.config/app/conf"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := f.remoteSubject(t); got != "save: dot_config/app/conf" {
		t.Errorf("remote subject = %q", got)
	}
	if got := gitRun(t, "--git-dir", f.remote, "show", "--name-only", "--format=", "main"); got != "dot_config/app/conf" {
		t.Errorf("files in save commit = %q, want dot_config/app/conf", got)
	}
	if got := readFileString(t, filepath.Join(f.repo, "dot_zshrc")); got != "old\n" {
		t.Errorf("untargeted dot_zshrc = %q, want old", got)
	}
}

func TestSave_CommitLeavesOtherStagedFiles(t *testing.T) {
	f := newGitRemoteFixture(t, map[string]string{"dot_bashrc": "old\n", "dot_zshrc": "old\n"}, GitAutomationConfig{AutoCommit: true})
	if err := os.WriteFile(filepath.Join(f.repo, "dot_bashrc"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, "-C", f.repo, "add", "dot_bashrc")
	f.writeHome(t, ".zshrc", "new\n")

	if err := f.app.Sync(context.Background(), "", false, false, false, false, []string{"~/.zshrc"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := gitRun(t, "-C", f.repo, "show", "--name-only", "--format=", "HEAD"); got != "dot_zshrc" {
		t.Errorf("files in save commit = %q, want dot_zshrc", got)
	}
	if got := gitRun(t, "-C", f.repo, "status", "--porcelain"); got != "M  dot_bashrc" {
		t.Errorf("git status = %q, want dot_bashrc still staged", got)
	}
}

func TestSave_UnchangedTargetCreatesNoCommit(t *testing.T) {
	f := newGitRemoteFixture(t, map[string]string{"dot_zshrc": "same\n"}, GitAutomationConfig{AutoCommit: true})
	f.writeHome(t, ".zshrc", "same\n")
	head := gitRun(t, "-C", f.repo, "rev-parse", "HEAD")
	buf := captureLog(t)

	if err := f.app.Sync(context.Background(), "", false, false, false, false, []string{"~/.zshrc"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := gitRun(t, "-C", f.repo, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved from %s to %s", head, got)
	}
	if got := gitRun(t, "-C", f.repo, "status", "--porcelain"); got != "" {
		t.Errorf("git status = %q, want clean", got)
	}
	if !strings.Contains(buf.String(), "nothing changed") {
		t.Errorf("log = %q, want nothing changed", buf.String())
	}
}
