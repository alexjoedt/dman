package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexjoedt/dman/internal/dotfile"
	"github.com/alexjoedt/dman/internal/profile"
)

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

// setupSymlinkFixture creates a temp home+repo+config for symlink tests.
// Returns App, home, repo paths.
func setupSymlinkFixture(t *testing.T) (*App, string, string) {
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

	a := &App{HomeDir: home, ConfigDir: cfgDir}
	if err := a.saveConfig(&Config{Path: repo}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	return a, home, repo
}

func TestAdd_RejectsSymlink(t *testing.T) {
	a, home, repo := setupSymlinkFixture(t)

	target := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	linkPath := filepath.Join(home, ".vim")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	err := a.Add(context.Background(), []string{linkPath}, "", false, false, false, false, false)
	if err == nil || !strings.Contains(err.Error(), linkPath) {
		t.Fatalf("Add error = %v, want refusal naming %s", err, linkPath)
	}
	if _, err := os.Lstat(filepath.Join(repo, "dot_vim")); err == nil {
		t.Error("symlink was added to repo")
	}
}

func TestAdd_SkipsSymlinkInsideDirectory(t *testing.T) {
	a, home, repo := setupSymlinkFixture(t)

	dir := filepath.Join(home, ".config", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "conf"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink("/nonexistent", filepath.Join(dir, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := a.Add(context.Background(), []string{dir}, "", false, false, false, false, false); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !isExist(filepath.Join(repo, "dot_config", "app", "conf")) {
		t.Error("regular file was not added")
	}
	if _, err := os.Lstat(filepath.Join(repo, "dot_config", "app", "link")); err == nil {
		t.Error("symlink was added to repo")
	}
}

// initGitRepo runs git init in dir so Apply (which calls getRepo) can succeed.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

func TestApply_SkipsSymlinks(t *testing.T) {
	a, home, repo := setupSymlinkFixture(t)
	initGitRepo(t, repo)

	// Repo symlink over a real home directory: skipped, directory kept.
	if err := os.Symlink(filepath.Join(home, ".config", "nvim"), filepath.Join(repo, "dot_vim")); err != nil {
		t.Fatalf("repo symlink: %v", err)
	}
	keep := filepath.Join(home, ".vim", "vimrc")
	if err := os.MkdirAll(filepath.Dir(keep), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(keep, []byte("set nocompatible\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Home symlink: skipped, its target is not written through.
	if err := os.WriteFile(filepath.Join(repo, "dot_zshrc"), []byte("from repo\n"), 0o644); err != nil {
		t.Fatalf("write repo file: %v", err)
	}
	target := filepath.Join(home, "old-dotfiles", "zshrc")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := []byte("precious\n")
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".zshrc")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Regular file: applied.
	if err := os.WriteFile(filepath.Join(repo, "dot_bashrc"), []byte("bash\n"), 0o644); err != nil {
		t.Fatalf("write repo file: %v", err)
	}

	if err := a.Apply(context.Background(), "", false, true, true, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !isExist(keep) {
		t.Errorf("home directory content was deleted")
	}
	if got, _ := os.ReadFile(target); !bytes.Equal(got, original) {
		t.Errorf("symlink target was overwritten: %q", got)
	}
	if !isSymlink(filepath.Join(home, ".zshrc")) {
		t.Errorf("home symlink was replaced")
	}
	if got, _ := os.ReadFile(filepath.Join(home, ".bashrc")); string(got) != "bash\n" {
		t.Errorf(".bashrc = %q, want applied", got)
	}
}

func TestAdd_SkipsRepoSymlink(t *testing.T) {
	a, home, repo := setupSymlinkFixture(t)

	outside := filepath.Join(t.TempDir(), "foo")
	original := []byte("precious\n")
	if err := os.WriteFile(outside, original, 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "dot_zshrc")); err != nil {
		t.Fatalf("repo symlink: %v", err)
	}
	zshrc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(zshrc, []byte("from home\n"), 0o644); err != nil {
		t.Fatalf("write home: %v", err)
	}

	if err := a.Add(context.Background(), []string{zshrc}, "", false, false, false, false, false); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got, _ := os.ReadFile(outside); !bytes.Equal(got, original) {
		t.Errorf("symlink target was overwritten: %q", got)
	}
}

func TestDiff_SkipsSymlinks(t *testing.T) {
	a, home, repo := setupDiffFixture(t, []byte("same\n"), nil)
	target := filepath.Join(home, "elsewhere")
	if err := os.WriteFile(target, []byte("other\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".zshrc")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	out := captureStdout(t, func() {
		if err := a.Diff(context.Background(), "", nil); err != nil {
			t.Fatalf("Diff: %v", err)
		}
	})
	if !strings.Contains(out, "up to date") {
		t.Errorf("symlink pair reported as changed: %q", out)
	}
	if computeChanged(&dotfile.Pair{Src: filepath.Join(repo, "dot_zshrc"), Dst: filepath.Join(home, ".zshrc")}, nil) {
		t.Error("computeChanged reports a symlink pair as changed")
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

// writeRepoFile writes content to <repo>/<layerDir>/<rel>, creating dirs.
func writeRepoFile(t *testing.T, dir, rel string, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupInheritFixture builds root, profiles/arch and profiles/arch-gridx
// (inheriting arch) with overlapping files:
//
//	dot_zshrc      in root, arch, arch-gridx
//	dot_archrc     in arch only
//	dot_gridxrc    in arch-gridx only
//	dot_rootrc     in root only
func setupInheritFixture(t *testing.T) (*App, string, string) {
	t.Helper()
	a, home, repo := setupDiffFixture(t, []byte("root zshrc\n"), nil)
	arch := profile.Dir(repo, "arch")
	gridx := profile.Dir(repo, "arch-gridx")
	writeRepoFile(t, repo, "dot_rootrc", "root\n")
	writeRepoFile(t, arch, "dot_zshrc", "arch zshrc\n")
	writeRepoFile(t, arch, "dot_archrc", "arch\n")
	writeRepoFile(t, gridx, "dot_zshrc", "gridx zshrc\n")
	writeRepoFile(t, gridx, "dot_gridxrc", "gridx\n")
	if err := profile.WriteMeta(repo, "arch-gridx", profile.Meta{Inherits: "arch"}); err != nil {
		t.Fatal(err)
	}
	return a, home, repo
}

func TestCollectTracked_InheritanceChain(t *testing.T) {
	a, home, repo := setupInheritFixture(t)
	cfg, err := a.readConfig()
	if err != nil {
		t.Fatal(err)
	}
	pairs, err := a.collectTracked(cfg, "arch-gridx")
	if err != nil {
		t.Fatalf("collectTracked: %v", err)
	}
	got := map[string]string{}
	for _, p := range dotfile.Merge(pairs) {
		rel, _ := filepath.Rel(repo, p.Src)
		got[p.Dst] = rel
	}
	want := map[string]string{
		filepath.Join(home, ".zshrc"):   filepath.Join("profiles", "arch-gridx", "dot_zshrc"),
		filepath.Join(home, ".archrc"):  filepath.Join("profiles", "arch", "dot_archrc"),
		filepath.Join(home, ".gridxrc"): filepath.Join("profiles", "arch-gridx", "dot_gridxrc"),
		filepath.Join(home, ".rootrc"):  "dot_rootrc",
	}
	if len(got) != len(want) {
		t.Fatalf("merged = %v; want %v", got, want)
	}
	for dst, src := range want {
		if got[dst] != src {
			t.Errorf("%s: src = %q; want %q", dst, got[dst], src)
		}
	}

	// The parent alone must not see the child's files.
	pairs, err = a.collectTracked(cfg, "arch")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range dotfile.Merge(pairs) {
		if strings.Contains(p.Src, "arch-gridx") {
			t.Errorf("parent chain includes child file %s", p.Src)
		}
	}
}

func TestApply_InheritedProfile(t *testing.T) {
	a, home, repo := setupInheritFixture(t)
	initGitRepo(t, repo)

	if err := a.Apply(context.Background(), "arch-gridx", false, true, true, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	checks := map[string]string{
		".zshrc":   "gridx zshrc\n",
		".archrc":  "arch\n",
		".gridxrc": "gridx\n",
		".rootrc":  "root\n",
	}
	for name, want := range checks {
		got, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			t.Errorf("%s not applied: %v", name, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s = %q; want %q", name, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "profile.json")); err == nil {
		t.Error("profile.json was applied to home")
	}
}

func TestApply_BrokenChainAborts(t *testing.T) {
	a, home, repo := setupInheritFixture(t)
	initGitRepo(t, repo)
	if err := os.RemoveAll(profile.Dir(repo, "arch")); err != nil {
		t.Fatal(err)
	}

	err := a.Apply(context.Background(), "arch-gridx", false, true, true, nil)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("Apply err = %v; want missing parent error", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gridxrc")); err == nil {
		t.Error("home was written despite broken chain")
	}
}

func TestSync_WritesToWinningLayer(t *testing.T) {
	a, home, repo := setupInheritFixture(t)
	// .archrc is only tracked in arch; a change in home must land there even
	// though the active profile is arch-gridx.
	if err := os.WriteFile(filepath.Join(home, ".archrc"), []byte("arch edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".zshrc", ".gridxrc", ".rootrc"} {
		src := map[string]string{".zshrc": "gridx zshrc\n", ".gridxrc": "gridx\n", ".rootrc": "root\n"}[name]
		if err := os.WriteFile(filepath.Join(home, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := a.Sync(context.Background(), "arch-gridx", false, false, false, false); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(profile.Dir(repo, "arch"), "dot_archrc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "arch edited\n" {
		t.Errorf("arch copy = %q; want edited content", got)
	}
	if _, err := os.Stat(filepath.Join(profile.Dir(repo, "arch-gridx"), "dot_archrc")); err == nil {
		t.Error("sync created a shadow copy in the child profile")
	}
}

func TestApply_StandaloneAncestorDropsRoot(t *testing.T) {
	a, home, repo := setupInheritFixture(t)
	initGitRepo(t, repo)
	if err := profile.WriteMeta(repo, "arch", profile.Meta{Standalone: true}); err != nil {
		t.Fatal(err)
	}

	if err := a.Apply(context.Background(), "arch-gridx", false, true, true, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	checks := map[string]string{
		".zshrc":   "gridx zshrc\n",
		".archrc":  "arch\n",
		".gridxrc": "gridx\n",
	}
	for name, want := range checks {
		got, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			t.Errorf("%s not applied: %v", name, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s = %q; want %q", name, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".rootrc")); err == nil {
		t.Error("root file applied for a standalone chain")
	}
}

func TestSync_StandaloneSkipsRoot(t *testing.T) {
	a, home, repo := setupInheritFixture(t)
	if err := profile.WriteMeta(repo, "arch", profile.Meta{Standalone: true}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".rootrc"), []byte("home edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := a.Sync(context.Background(), "arch", false, false, false, false); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(repo, "dot_rootrc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "root\n" {
		t.Errorf("root copy = %q; standalone sync must not touch root", got)
	}
}

func TestApply_StandaloneRootIncludes(t *testing.T) {
	a, home, repo := setupInheritFixture(t)
	initGitRepo(t, repo)
	a.HomeMode = 0o755
	writeRepoFile(t, repo, "dot_config/nvim/init.lua", "root nvim\n")
	if err := profile.WriteMeta(repo, "arch", profile.Meta{Standalone: true, Root: []string{"~/.config/nvim"}}); err != nil {
		t.Fatal(err)
	}

	if err := a.Apply(context.Background(), "arch", false, true, true, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(home, ".config", "nvim", "init.lua"))
	if err != nil || string(got) != "root nvim\n" {
		t.Errorf("init.lua = %q, %v; want root copy", got, err)
	}
	for _, name := range []string{".rootrc"} {
		if _, err := os.Stat(filepath.Join(home, name)); err == nil {
			t.Errorf("%s applied although not included", name)
		}
	}
}

func TestSync_StandaloneRootIncludesWritesRoot(t *testing.T) {
	a, home, repo := setupInheritFixture(t)
	writeRepoFile(t, repo, "dot_config/nvim/init.lua", "root nvim\n")
	if err := profile.WriteMeta(repo, "arch", profile.Meta{Standalone: true, Root: []string{"~/.config/nvim"}}); err != nil {
		t.Fatal(err)
	}
	writeRepoFile(t, home, ".config/nvim/init.lua", "home nvim\n")
	if err := os.WriteFile(filepath.Join(home, ".rootrc"), []byte("home edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := a.Sync(context.Background(), "arch", false, false, false, false); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(repo, "dot_config", "nvim", "init.lua"))
	if string(got) != "home nvim\n" {
		t.Errorf("root init.lua = %q; want synced content", got)
	}
	got, _ = os.ReadFile(filepath.Join(repo, "dot_rootrc"))
	if string(got) != "root\n" {
		t.Errorf("root .rootrc = %q; must stay untouched", got)
	}
}

func TestCollectTracked_RootIncludes(t *testing.T) {
	tests := []struct {
		name    string
		parent  profile.Meta
		child   profile.Meta
		profile string
		want    []string
	}{
		{name: "child inherits parent entries", parent: profile.Meta{Standalone: true, Root: []string{"~/.rootrc"}}, child: profile.Meta{Inherits: "arch", Root: []string{"~/.config/nvim"}}, profile: "arch-gridx", want: []string{".rootrc", ".config/nvim/init.lua", ".zshrc", ".archrc", ".gridxrc"}},
		{name: "file entry matches only that file", parent: profile.Meta{Standalone: true, Root: []string{"~/.zshrc"}}, child: profile.Meta{Inherits: "arch"}, profile: "arch", want: []string{".zshrc", ".archrc"}},
		{name: "sibling prefix not matched", parent: profile.Meta{Standalone: true, Root: []string{"~/.config/nv"}}, child: profile.Meta{Inherits: "arch"}, profile: "arch", want: []string{".zshrc", ".archrc"}},
		{name: "empty list drops root", parent: profile.Meta{Standalone: true}, child: profile.Meta{Inherits: "arch"}, profile: "arch", want: []string{".zshrc", ".archrc"}},
		{name: "non-standalone ignores list", parent: profile.Meta{Root: []string{"~/.rootrc"}}, child: profile.Meta{Inherits: "arch"}, profile: "arch", want: []string{".rootrc", ".config/nvim/init.lua", ".zshrc", ".archrc"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, home, repo := setupInheritFixture(t)
			writeRepoFile(t, repo, "dot_config/nvim/init.lua", "root nvim\n")
			if err := profile.WriteMeta(repo, "arch", tt.parent); err != nil {
				t.Fatal(err)
			}
			if err := profile.WriteMeta(repo, "arch-gridx", tt.child); err != nil {
				t.Fatal(err)
			}
			cfg, err := a.readConfig()
			if err != nil {
				t.Fatal(err)
			}
			pairs, err := a.collectTracked(cfg, tt.profile)
			if err != nil {
				t.Fatalf("collectTracked: %v", err)
			}
			got := map[string]bool{}
			for _, p := range dotfile.Merge(pairs) {
				rel, _ := filepath.Rel(home, p.Dst)
				got[rel] = true
			}
			if len(got) != len(tt.want) {
				t.Fatalf("dsts = %v; want %v", got, tt.want)
			}
			for _, w := range tt.want {
				if !got[w] {
					t.Errorf("missing %s in %v", w, got)
				}
			}
		})
	}
}

func TestCollectTracked_ExcludedRootConflictIgnored(t *testing.T) {
	a, _, repo := setupInheritFixture(t)
	writeRepoFile(t, repo, "dot_config/foo", "plain\n")
	writeRepoFile(t, repo, "dot_config/foo.crypt", "enc\n")
	if err := profile.WriteMeta(repo, "arch", profile.Meta{Standalone: true, Root: []string{"~/.zshrc"}}); err != nil {
		t.Fatal(err)
	}
	cfg, err := a.readConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.collectTracked(cfg, "arch"); err != nil {
		t.Fatalf("collectTracked: %v", err)
	}
	if err := profile.WriteMeta(repo, "arch", profile.Meta{Standalone: true, Root: []string{"~/.config"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.collectTracked(cfg, "arch"); err == nil {
		t.Fatal("want conflict error for included root files")
	}
}

func TestAdd_TargetFollowsStandaloneProfile(t *testing.T) {
	tests := []struct {
		name        string
		active      string
		standalone  bool
		profileFlag string
		root        bool
		wantDir     string // relative to repo; "" is the root
		wantErr     bool
	}{
		{name: "active standalone", active: "arch", standalone: true, wantDir: filepath.Join("profiles", "arch")},
		{name: "active overlay", active: "arch", wantDir: ""},
		{name: "flag wins", active: "arch", standalone: true, profileFlag: "arch-gridx", wantDir: filepath.Join("profiles", "arch-gridx")},
		{name: "root wins", active: "arch", standalone: true, root: true, wantDir: ""},
		{name: "root and profile conflict", active: "arch", standalone: true, root: true, profileFlag: "arch-gridx", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, home, repo := setupInheritFixture(t)
			if err := profile.WriteMeta(repo, "arch", profile.Meta{Standalone: tt.standalone}); err != nil {
				t.Fatal(err)
			}
			if err := a.saveConfig(&Config{Path: repo, Profile: tt.active}); err != nil {
				t.Fatal(err)
			}
			src := filepath.Join(home, ".newrc")
			if err := os.WriteFile(src, []byte("new\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			err := a.Add(context.Background(), []string{src}, tt.profileFlag, tt.root, false, false, false, false)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Add: %v", err)
			}
			if _, err := os.Stat(filepath.Join(repo, tt.wantDir, "dot_newrc")); err != nil {
				t.Errorf("dot_newrc not in %q: %v", tt.wantDir, err)
			}
		})
	}
}

func TestReadConfig_CleansPath(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	a := &App{HomeDir: dir, ConfigDir: cfgDir}
	if err := a.saveConfig(&Config{Path: dir + "/repo/", Profile: "default"}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	cfg, err := a.readConfig()
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if want := filepath.Join(dir, "repo"); cfg.Path != want {
		t.Errorf("Path = %q, want %q", cfg.Path, want)
	}
}

func TestInit_StoresAbsoluteCleanPath(t *testing.T) {
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin")
	initGitRepo(t, origin)
	if err := os.WriteFile(filepath.Join(origin, "dot_zshrc"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write dot_zshrc: %v", err)
	}
	for _, args := range [][]string{
		{"-C", origin, "add", "."},
		{"-C", origin, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Chdir(dir)
	a := &App{HomeDir: dir, ConfigDir: cfgDir}
	if err := a.Init(context.Background(), origin, "dots/"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	cfg, err := a.readConfig()
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(dir, "dots"))
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	got, err := filepath.EvalSymlinks(cfg.Path)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	if !filepath.IsAbs(cfg.Path) || got != want {
		t.Errorf("Path = %q, want absolute path to %q", cfg.Path, want)
	}
}

func TestSync_DanglingRepoSymlinkDoesNotAbort(t *testing.T) {
	a, home, repo := setupSymlinkFixture(t)

	// Repo link points somewhere that only exists on another machine.
	if err := os.Symlink("/nonexistent/on/this/host", filepath.Join(repo, "dot_vim")); err != nil {
		t.Fatalf("repo symlink: %v", err)
	}
	if err := os.Symlink("/nonexistent/on/this/host", filepath.Join(home, ".vim")); err != nil {
		t.Fatalf("home symlink: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "dot_zshrc"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := a.Sync(context.Background(), "", false, false, false, false); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(repo, "dot_zshrc"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "new\n" {
		t.Errorf("dot_zshrc = %q, want %q", got, "new\n")
	}
}

func TestSync_SkipsRepoSymlink(t *testing.T) {
	a, home, repo := setupSymlinkFixture(t)

	target := filepath.Join(home, "elsewhere")
	if err := os.WriteFile(target, []byte("target\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(repo, "dot_zshrc")); err != nil {
		t.Fatalf("repo symlink: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte("real file\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := a.Sync(context.Background(), "", false, false, false, false); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if !isSymlink(filepath.Join(repo, "dot_zshrc")) {
		t.Errorf("repo symlink was replaced")
	}
	if got, _ := os.ReadFile(target); string(got) != "target\n" {
		t.Errorf("symlink target was written through: %q", got)
	}
}

func TestInit_RemovesCloneWhenNotDotfileRepo(t *testing.T) {
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin")
	initGitRepo(t, origin)
	if err := os.WriteFile(filepath.Join(origin, "README.md"), []byte("no dotfiles\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, args := range [][]string{
		{"-C", origin, "add", "."},
		{"-C", origin, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	a := &App{HomeDir: dir, ConfigDir: cfgDir}
	dest := filepath.Join(dir, "dots")
	if err := a.Init(context.Background(), origin, dest); err == nil {
		t.Fatal("Init: want error for repo without dotfiles")
	}
	if isExist(dest) {
		t.Errorf("clone left behind at %s", dest)
	}
	if _, err := a.readConfig(); err == nil {
		t.Error("config was written despite failed init")
	}
}

func TestSafeToRemove(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "dotfiles")
	if err := os.MkdirAll(filepath.Join(repo, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	notRepo := filepath.Join(home, "docs")
	if err := os.MkdirAll(notRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, path string
		wantErr    bool
	}{
		{"empty", "", true},
		{"dot", ".", true},
		{"root", "/", true},
		{"home", home, true},
		{"outside", t.TempDir(), true},
		{"not a repo", notRepo, true},
		{"repo", repo, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := safeToRemove(tc.path, home); (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestInit_RemovesCloneWhenSaveConfigFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks do not apply to root")
	}
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin")
	initGitRepo(t, origin)
	if err := os.WriteFile(filepath.Join(origin, "dot_bashrc"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, args := range [][]string{
		{"-C", origin, "add", "."},
		{"-C", origin, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(cfgDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(cfgDir, 0o755) })

	a := &App{HomeDir: dir, ConfigDir: cfgDir}
	dest := filepath.Join(dir, "dots")
	if err := a.Init(context.Background(), origin, dest); err == nil {
		t.Fatal("Init: want error when config cannot be saved")
	}
	if isExist(dest) {
		t.Errorf("clone left behind at %s", dest)
	}
}
