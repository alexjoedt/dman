package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type remoteFixture struct {
	remote, a, b string
}

func gitRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitFile(t *testing.T, dir, name, content string) {
	t.Helper()
	write(t, dir, name, content)
	gitRun(t, "-C", dir, "add", name)
	gitRun(t, "-C", dir, "commit", "-q", "-m", "change "+name)
}

// newRemoteFixture builds a bare remote on main and two clones, both at the
// same seed commit that holds shared.txt. Git config is isolated from the user's.
func newRemoteFixture(t *testing.T) *remoteFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH, skipping git test")
	}
	root := t.TempDir()
	gitconfig := filepath.Join(root, "gitconfig")
	write(t, root, "gitconfig", "[user]\n\tname = t\n\temail = t@t\n[init]\n\tdefaultBranch = main\n")
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}

	f := &remoteFixture{remote: filepath.Join(root, "remote.git"), a: filepath.Join(root, "a"), b: filepath.Join(root, "b")}
	gitRun(t, "init", "-q", "--bare", f.remote)
	gitRun(t, "init", "-q", f.a)
	gitRun(t, "-C", f.a, "remote", "add", "origin", f.remote)
	commitFile(t, f.a, "shared.txt", "base\n")
	gitRun(t, "-C", f.a, "push", "-q", "-u", "origin", "main")
	gitRun(t, "clone", "-q", f.remote, f.b)
	return f
}

func repoAt(t *testing.T, path string) *Repo {
	t.Helper()
	r, err := GetRepo(path)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPush_ConflictAbortsRebase(t *testing.T) {
	f := newRemoteFixture(t)
	commitFile(t, f.b, "shared.txt", "remote\n")
	gitRun(t, "-C", f.b, "push", "-q")
	commitFile(t, f.a, "shared.txt", "local\n")
	head := gitRun(t, "-C", f.a, "rev-parse", "HEAD")

	err := repoAt(t, f.a).Push(context.Background())
	if err == nil {
		t.Fatal("Push returned nil on a conflicting remote change")
	}
	if msg := err.Error(); !strings.Contains(msg, "push not done") || !strings.Contains(msg, "aborted") {
		t.Errorf("error does not say push not done and rebase aborted: %v", err)
	}
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		if _, statErr := os.Stat(filepath.Join(f.a, ".git", name)); statErr == nil {
			t.Errorf(".git/%s still exists", name)
		}
	}
	if got := gitRun(t, "-C", f.a, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD = %s, want %s", got, head)
	}
	if st := gitRun(t, "-C", f.a, "status", "--porcelain"); st != "" {
		t.Errorf("working tree not clean:\n%s", st)
	}
}

func TestPush_FailedPullBeforeRebaseKeepsError(t *testing.T) {
	f := newRemoteFixture(t)
	commitFile(t, f.b, "other.txt", "x\n")
	gitRun(t, "-C", f.b, "push", "-q")
	commitFile(t, f.a, "shared.txt", "local\n")
	gitRun(t, "-C", f.a, "config", "remote.origin.pushurl", f.remote)
	gitRun(t, "-C", f.a, "remote", "set-url", "origin", filepath.Join(f.a, "missing.git"))

	err := repoAt(t, f.a).Push(context.Background())
	if err == nil {
		t.Fatal("Push returned nil with an unreachable remote")
	}
	if !strings.Contains(err.Error(), "push not done") {
		t.Errorf("error does not say push not done: %v", err)
	}
	if strings.Contains(err.Error(), "aborted") {
		t.Errorf("error claims an abort without a rebase: %v", err)
	}
}

func TestPush_RetriesAfterRejection(t *testing.T) {
	f := newRemoteFixture(t)
	commitFile(t, f.b, "other.txt", "x\n")
	gitRun(t, "-C", f.b, "push", "-q")
	commitFile(t, f.a, "mine.txt", "y\n")

	if err := repoAt(t, f.a).Push(context.Background()); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if n := gitRun(t, "--git-dir", f.remote, "rev-list", "--count", "main"); n != "3" {
		t.Errorf("remote has %s commits, want 3", n)
	}
	files := gitRun(t, "--git-dir", f.remote, "ls-tree", "-r", "--name-only", "main")
	for _, want := range []string{"mine.txt", "other.txt", "shared.txt"} {
		if !strings.Contains(files, want) {
			t.Errorf("remote lacks %s:\n%s", want, files)
		}
	}
}

func TestIsRejectedPush(t *testing.T) {
	tests := []struct {
		name, stderr string
		want         bool
	}{
		{"marker alone", " ! [rejected]        main -> main (fetch first)\n", true},
		{"marker with german hint", " ! [rejected]        main -> main (non-fast-forward)\nHinweis: Aktualisieren Sie Ihr lokales Repository\n", true},
		{"marker with french hint", "! [rejected] main -> main\nastuce : Mettez a jour votre depot local\n", true},
		{"unrelated failure", "fatal: Could not read from remote repository.\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRejectedPush(tt.stderr); got != tt.want {
				t.Errorf("isRejectedPush = %v, want %v", got, tt.want)
			}
		})
	}
}
