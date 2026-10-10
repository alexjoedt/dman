package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexjoedt/dman/internal/snapshot"
)

// snapshotEnv wires an App against a temporary home, config, and snapshot dir.
func snapshotEnv(t *testing.T) *App {
	t.Helper()

	root := t.TempDir()
	home := filepath.Join(root, "home")
	cfgDir := filepath.Join(root, "config")
	for _, d := range []string{home, cfgDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	a := &App{HomeDir: home, HomeMode: 0o755, ConfigDir: cfgDir}
	cfg := &Config{
		Path:    filepath.Join(root, "repo"),
		Profile: "default",
		Snapshots: &SnapshotConfig{
			Enabled: true,
			Path:    filepath.Join(root, "snapshots"),
		},
	}
	if err := a.saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	return a
}

// writeHome creates a file under the app's home directory.
func writeHome(t *testing.T, a *App, rel, content string, mode os.FileMode) string {
	t.Helper()
	abs := filepath.Join(a.HomeDir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(abs, mode); err != nil {
		t.Fatal(err)
	}
	return abs
}

// snap takes a snapshot of the given home-relative files and returns its ID.
func snap(t *testing.T, a *App, rels ...string) string {
	t.Helper()

	cfg, err := a.readConfig()
	if err != nil {
		t.Fatal(err)
	}
	store, err := a.snapshotStore(cfg)
	if err != nil {
		t.Fatal(err)
	}

	abs := make([]string, len(rels))
	for i, rel := range rels {
		abs[i] = filepath.Join(a.HomeDir, rel)
	}
	meta, err := store.Create(context.Background(), a.HomeDir, abs, "test")
	if err != nil {
		t.Fatal(err)
	}
	return meta.ID
}

func listSnapshots(t *testing.T, a *App) []snapshot.Meta {
	t.Helper()

	cfg, err := a.readConfig()
	if err != nil {
		t.Fatal(err)
	}
	store, err := a.snapshotStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	metas, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	return metas
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSnapshotRestoreRewritesModifiedFile(t *testing.T) {
	a := snapshotEnv(t)
	abs := writeHome(t, a, ".zshrc", "original\n", 0o640)
	id := snap(t, a, ".zshrc")

	writeHome(t, a, ".zshrc", "broken\n", 0o644)

	if err := a.SnapshotRestore(context.Background(), id, []string{".zshrc"}); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got := readFile(t, abs); got != "original\n" {
		t.Errorf("content = %q, want %q", got, "original\n")
	}
	fi, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o640 {
		t.Errorf("mode = %o, want 640", got)
	}
}

func TestSnapshotRestoreRecreatesDeletedFile(t *testing.T) {
	a := snapshotEnv(t)
	abs := writeHome(t, a, ".config/nvim/init.lua", "vim.opt.number = true\n", 0o644)
	id := snap(t, a, ".config/nvim/init.lua")

	if err := os.RemoveAll(filepath.Join(a.HomeDir, ".config")); err != nil {
		t.Fatal(err)
	}

	if err := a.SnapshotRestore(context.Background(), id, []string{".config/nvim/init.lua"}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := readFile(t, abs); got != "vim.opt.number = true\n" {
		t.Errorf("content = %q", got)
	}
}

func TestSnapshotRestoreSkipsUnchangedAndTakesNoBackup(t *testing.T) {
	a := snapshotEnv(t)
	writeHome(t, a, ".zshrc", "original\n", 0o644)
	id := snap(t, a, ".zshrc")

	before := len(listSnapshots(t, a))

	// The home file still matches the snapshot, so this is a no-op.
	if err := a.SnapshotRestore(context.Background(), id, []string{".zshrc"}); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if after := len(listSnapshots(t, a)); after != before {
		t.Errorf("snapshot count = %d, want %d: a no-op restore must not back anything up", after, before)
	}
}

func TestSnapshotRestoreBacksUpBeforeOverwriting(t *testing.T) {
	a := snapshotEnv(t)
	writeHome(t, a, ".zshrc", "original\n", 0o644)
	id := snap(t, a, ".zshrc")
	writeHome(t, a, ".zshrc", "current\n", 0o644)

	if err := a.SnapshotRestore(context.Background(), id, []string{".zshrc"}); err != nil {
		t.Fatalf("restore: %v", err)
	}

	metas := listSnapshots(t, a)
	if len(metas) != 2 {
		t.Fatalf("snapshot count = %d, want 2", len(metas))
	}

	backup := metas[len(metas)-1]
	if want := "auto: before restore " + id; backup.Message != want {
		t.Errorf("message = %q, want %q", backup.Message, want)
	}

	// The backup must hold what was on disk just before the restore.
	if err := a.SnapshotRestore(context.Background(), backup.ID, []string{".zshrc"}); err != nil {
		t.Fatalf("restore backup: %v", err)
	}
	if got := readFile(t, filepath.Join(a.HomeDir, ".zshrc")); got != "current\n" {
		t.Errorf("backup content = %q, want %q", got, "current\n")
	}
}

func TestSnapshotRestoreUnknownFileWritesNothing(t *testing.T) {
	a := snapshotEnv(t)
	writeHome(t, a, ".zshrc", "original\n", 0o644)
	id := snap(t, a, ".zshrc")
	writeHome(t, a, ".zshrc", "current\n", 0o644)

	before := len(listSnapshots(t, a))

	err := a.SnapshotRestore(context.Background(), id, []string{".zshrc", ".nope"})
	if err == nil {
		t.Fatal("want an error for a file that is not in the snapshot")
	}
	if !strings.Contains(err.Error(), ".nope") || !strings.Contains(err.Error(), id) {
		t.Errorf("error = %q, want it to name the file and the snapshot", err)
	}

	// The valid target must not have been touched either.
	if got := readFile(t, filepath.Join(a.HomeDir, ".zshrc")); got != "current\n" {
		t.Errorf("content = %q: a rejected restore must write nothing", got)
	}
	if after := len(listSnapshots(t, a)); after != before {
		t.Errorf("snapshot count = %d, want %d: no backup on a rejected restore", after, before)
	}
}

func TestSnapshotRestoreAcceptsEveryTargetForm(t *testing.T) {
	a := snapshotEnv(t)
	abs := writeHome(t, a, ".zshrc", "original\n", 0o644)
	id := snap(t, a, ".zshrc")

	for _, target := range []string{".zshrc", "~/.zshrc", abs} {
		writeHome(t, a, ".zshrc", "broken\n", 0o644)
		if err := a.SnapshotRestore(context.Background(), id, []string{target}); err != nil {
			t.Fatalf("restore %q: %v", target, err)
		}
		if got := readFile(t, abs); got != "original\n" {
			t.Errorf("restore %q left %q", target, got)
		}
	}
}

func TestSnapshotRestoreRefusesSymlink(t *testing.T) {
	a := snapshotEnv(t)
	writeHome(t, a, ".zshrc", "original\n", 0o644)
	id := snap(t, a, ".zshrc")

	// Replace the home file with a symlink to another file. Writing through it
	// would clobber the target, and the backup skips symlinks entirely.
	target := writeHome(t, a, "real-config", "precious\n", 0o644)
	link := filepath.Join(a.HomeDir, ".zshrc")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	before := len(listSnapshots(t, a))

	err := a.SnapshotRestore(context.Background(), id, []string{".zshrc"})
	if err == nil {
		t.Fatal("want an error when the restore target is a symlink")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error = %q, want it to name the symlink", err)
	}

	if got := readFile(t, target); got != "precious\n" {
		t.Errorf("link target = %q, the refused restore must not write through the link", got)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink itself was replaced")
	}
	if after := len(listSnapshots(t, a)); after != before {
		t.Errorf("snapshot count = %d, want %d: no backup on a refused restore", after, before)
	}
}

func snapshotFiles(t *testing.T, a *App, id string) map[string]snapshot.File {
	t.Helper()
	cfg, err := a.readConfig()
	if err != nil {
		t.Fatal(err)
	}
	store, err := a.snapshotStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	files, err := store.Files(id)
	if err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]snapshot.File, len(files))
	for _, f := range files {
		byPath[f.Path] = f
	}
	return byPath
}

func TestSnapshotRestoreRemovesAbsentFile(t *testing.T) {
	a := snapshotEnv(t)
	id := snap(t, a, ".newrc")
	abs := writeHome(t, a, ".newrc", "created\n", 0o644)

	if err := a.SnapshotRestore(context.Background(), id, []string{".newrc"}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Lstat(abs); !os.IsNotExist(err) {
		t.Fatalf("Lstat after restore = %v; want the file removed", err)
	}

	metas := listSnapshots(t, a)
	if len(metas) != 2 {
		t.Fatalf("snapshot count = %d, want 2", len(metas))
	}
	backup := metas[len(metas)-1]
	if f := snapshotFiles(t, a, backup.ID)[".newrc"]; f.Absent || f.Checksum == "" {
		t.Fatalf("backup entry = %+v; want the removed content", f)
	}
	if err := a.SnapshotRestore(context.Background(), backup.ID, []string{".newrc"}); err != nil {
		t.Fatalf("restore backup: %v", err)
	}
	if got := readFile(t, abs); got != "created\n" {
		t.Errorf("backup content = %q, want %q", got, "created\n")
	}
}

func TestSnapshotRestoreDedupesTargets(t *testing.T) {
	a := snapshotEnv(t)
	id := snap(t, a, ".newrc")
	abs := writeHome(t, a, ".newrc", "created\n", 0o644)

	if err := a.SnapshotRestore(context.Background(), id, []string{".newrc", abs}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Lstat(abs); !os.IsNotExist(err) {
		t.Fatalf("Lstat after restore = %v; want the file removed", err)
	}
}

func TestSnapshotRestoreFailsOnUnstatablePath(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	a := snapshotEnv(t)
	id := snap(t, a, ".cfg/newrc")
	abs := writeHome(t, a, ".cfg/newrc", "created\n", 0o644)
	dir := filepath.Dir(abs)
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	before := len(listSnapshots(t, a))

	err := a.SnapshotRestore(context.Background(), id, []string{".cfg/newrc"})
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err = %v; want the stat error, not 'already absent'", err)
	}
	if after := len(listSnapshots(t, a)); after != before {
		t.Errorf("snapshot count = %d, want %d", after, before)
	}
}

func TestSnapshotRestoreSkipsAlreadyAbsentFile(t *testing.T) {
	a := snapshotEnv(t)
	id := snap(t, a, ".newrc")
	before := len(listSnapshots(t, a))

	if err := a.SnapshotRestore(context.Background(), id, []string{".newrc"}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if after := len(listSnapshots(t, a)); after != before {
		t.Errorf("snapshot count = %d, want %d: nothing to remove, no backup", after, before)
	}
}

func TestSnapshotRestoreBackupRecordsMissingFileAsAbsent(t *testing.T) {
	a := snapshotEnv(t)
	abs := writeHome(t, a, ".zshrc", "original\n", 0o644)
	id := snap(t, a, ".zshrc")
	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}

	if err := a.SnapshotRestore(context.Background(), id, []string{".zshrc"}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	metas := listSnapshots(t, a)
	backup := metas[len(metas)-1]
	if f := snapshotFiles(t, a, backup.ID)[".zshrc"]; !f.Absent {
		t.Fatalf("backup entry = %+v; want .zshrc recorded absent", f)
	}

	// Undoing the restore removes the file again.
	if err := a.SnapshotRestore(context.Background(), backup.ID, []string{".zshrc"}); err != nil {
		t.Fatalf("restore backup: %v", err)
	}
	if _, err := os.Lstat(abs); !os.IsNotExist(err) {
		t.Errorf("Lstat after undo = %v; want the file removed", err)
	}
}

func TestSnapshotRestoreRefusesSymlinkForAbsentEntry(t *testing.T) {
	a := snapshotEnv(t)
	id := snap(t, a, ".newrc")
	target := writeHome(t, a, "real-config", "precious\n", 0o644)
	link := filepath.Join(a.HomeDir, ".newrc")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	err := a.SnapshotRestore(context.Background(), id, []string{".newrc"})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("err = %v; want the symlink refusal", err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was removed")
	}
	if got := readFile(t, target); got != "precious\n" {
		t.Errorf("link target = %q", got)
	}
}

func TestSnapshotRestoreRefusedWhenDisabled(t *testing.T) {
	a := snapshotEnv(t)
	writeHome(t, a, ".zshrc", "original\n", 0o644)
	id := snap(t, a, ".zshrc")

	cfg, err := a.readConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Snapshots.Enabled = false
	if err := a.saveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	if err := a.SnapshotRestore(context.Background(), id, []string{".zshrc"}); err == nil {
		t.Fatal("want an error when snapshots are disabled")
	}
}

func TestApplySnapshotsOnlyChangedFiles(t *testing.T) {
	a := snapshotEnv(t)
	cfg, err := a.readConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, cfg.Path)
	for name, content := range map[string]string{"dot_same": "same\n", "dot_changed": "new\n"} {
		if err := os.WriteFile(filepath.Join(cfg.Path, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeHome(t, a, ".same", "same\n", 0o644)
	writeHome(t, a, ".changed", "old\n", 0o644)

	if err := a.Apply(context.Background(), "", false, true, false, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	metas := listSnapshots(t, a)
	if len(metas) != 1 {
		t.Fatalf("snapshots = %d; want 1", len(metas))
	}
	if n := metas[0].FileCount; n != 1 {
		t.Errorf("snapshot files = %d; want only the changed file", n)
	}

	if err := a.Apply(context.Background(), "", false, true, false, nil); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if n := len(listSnapshots(t, a)); n != 1 {
		t.Errorf("no-op apply created a snapshot, total %d", n)
	}
}

func TestApplySnapshotRecordsCreatedFileAsAbsent(t *testing.T) {
	a := snapshotEnv(t)
	cfg, err := a.readConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, cfg.Path)
	for name, content := range map[string]string{"dot_newrc": "new\n", "dot_changed": "new\n"} {
		if err := os.WriteFile(filepath.Join(cfg.Path, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeHome(t, a, ".changed", "old\n", 0o644)

	if err := a.Apply(context.Background(), "", false, true, false, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	metas := listSnapshots(t, a)
	if len(metas) != 1 {
		t.Fatalf("snapshots = %d; want 1", len(metas))
	}
	store, err := a.snapshotStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	files, err := store.Files(metas[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	absent := map[string]bool{}
	for _, f := range files {
		absent[f.Path] = f.Absent
	}
	if len(files) != 2 || !absent[".newrc"] || absent[".changed"] {
		t.Errorf("manifest = %+v; want .newrc absent and .changed present", files)
	}
}

func TestSnapshotCreateRecordsMissingTrackedFileAsAbsent(t *testing.T) {
	a := snapshotEnv(t)
	cfg, err := a.readConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, cfg.Path)
	for _, name := range []string{"dot_here", "dot_gone"} {
		if err := os.WriteFile(filepath.Join(cfg.Path, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := a.SnapshotCreate(context.Background(), "m"); err != nil {
		t.Fatal(err)
	}
	if n := len(listSnapshots(t, a)); n != 0 {
		t.Fatalf("snapshot written with nothing on disk: %d", n)
	}

	writeHome(t, a, ".here", "x\n", 0o644)
	if err := a.SnapshotCreate(context.Background(), "m"); err != nil {
		t.Fatal(err)
	}
	metas := listSnapshots(t, a)
	if len(metas) != 1 || metas[0].FileCount != 2 {
		t.Fatalf("metas = %+v; want one snapshot with 2 entries", metas)
	}
}

func TestSnapshotRestoreWholeSnapshotWithoutFileList(t *testing.T) {
	a := snapshotEnv(t)
	writeHome(t, a, ".zshrc", "original\n", 0o644)
	id := snap(t, a, ".zshrc", ".newrc")
	zshrc := writeHome(t, a, ".zshrc", "broken\n", 0o644)
	newrc := writeHome(t, a, ".newrc", "created\n", 0o644)

	if err := a.SnapshotRestore(context.Background(), id, nil); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := readFile(t, zshrc); got != "original\n" {
		t.Errorf(".zshrc = %q, want original", got)
	}
	if _, err := os.Lstat(newrc); !os.IsNotExist(err) {
		t.Errorf("Lstat .newrc = %v; want the absent file removed", err)
	}

	before := len(listSnapshots(t, a))
	if err := a.SnapshotRestore(context.Background(), id, nil); err != nil {
		t.Fatalf("second restore: %v", err)
	}
	if got := len(listSnapshots(t, a)); got != before {
		t.Errorf("snapshot count = %d, want %d: a no-op full restore takes no backup", got, before)
	}
}
