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
