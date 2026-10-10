package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/alexjoedt/dman/internal/dotfile"
	"github.com/alexjoedt/dman/internal/hash"
	"github.com/alexjoedt/dman/internal/snapshot"
	"github.com/alexjoedt/log"
)

func (a *App) snapshotStore(cfg *Config) (*snapshot.Store, error) {
	if !cfg.Snapshots.Enabled {
		return nil, fmt.Errorf("snapshots are not enabled; set snapshots.enabled=true in %s",
			filepath.Join(a.ConfigDir, configFileName))
	}
	dir := cfg.Snapshots.Path
	if dir == "" {
		dir = filepath.Join(a.HomeDir, ".local", "state", "dman", "snapshots")
	}
	return snapshot.NewStore(dir)
}

// autoSnapshot captures the current home-side contents of pairs before they are
// overwritten. Destinations that do not exist yet are recorded as absent, so a
// restore can remove what the write created.
func (a *App) autoSnapshot(ctx context.Context, cfg *Config, pairs []dotfile.Pair, message string) (snapshot.Meta, error) {
	if len(pairs) == 0 {
		return snapshot.Meta{}, nil
	}
	targets := make([]string, len(pairs))
	for i, p := range pairs {
		targets[i] = p.Dst
	}
	store, err := a.snapshotStore(cfg)
	if err != nil {
		return snapshot.Meta{}, err
	}
	return store.Create(ctx, a.HomeDir, targets, message)
}

// SnapshotCreate captures a full snapshot of all currently tracked dotfiles.
func (a *App) SnapshotCreate(ctx context.Context, message string) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	store, err := a.snapshotStore(cfg)
	if err != nil {
		return err
	}

	pairs, err := a.collectTracked(cfg, cfg.Profile)
	if err != nil {
		return err
	}

	var targets []string
	onDisk := false
	for _, p := range dotfile.Merge(pairs) {
		targets = append(targets, p.Dst)
		onDisk = onDisk || isExist(p.Dst)
	}
	if !onDisk {
		log.Warn("no tracked dotfiles found on disk; nothing to snapshot")
		return nil
	}

	meta, err := store.Create(ctx, a.HomeDir, targets, message)
	if err != nil {
		return err
	}
	log.Success(fmt.Sprintf("snapshot created: %s (%d file(s))", meta.ID, meta.FileCount))
	return nil
}

// SnapshotList prints all snapshots in a table.
func (a *App) SnapshotList(ctx context.Context) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	store, err := a.snapshotStore(cfg)
	if err != nil {
		return err
	}
	snaps, err := store.List()
	if err != nil {
		return err
	}
	if len(snaps) == 0 {
		fmt.Println("no snapshots")
		return nil
	}
	fmt.Printf("%-32s  %-20s  %5s  %s\n", "ID", "DATE", "FILES", "MESSAGE")
	fmt.Printf("%-32s  %-20s  %5s  %s\n", strings.Repeat("-", 32), strings.Repeat("-", 20), "-----", "-------")
	for _, s := range snaps {
		fmt.Printf("%-32s  %-20s  %5d  %s\n",
			s.ID,
			s.CreatedAt.Local().Format("2006-01-02 15:04:05"),
			s.FileCount,
			s.Message,
		)
	}
	return nil
}

// SnapshotShow prints the files contained in a snapshot.
func (a *App) SnapshotShow(ctx context.Context, id string) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	store, err := a.snapshotStore(cfg)
	if err != nil {
		return err
	}
	files, err := store.Files(id)
	if err != nil {
		return err
	}
	fmt.Printf("%-12s  %s\n", "CHECKSUM", "PATH")
	fmt.Printf("%-12s  %s\n", strings.Repeat("-", 12), "----")
	for _, f := range files {
		sum := "(absent)"
		if !f.Absent {
			sum = f.Checksum[:12]
		}
		fmt.Printf("%-12s  %s\n", sum, f.Path)
	}
	return nil
}

// SnapshotCat streams the blob for the given checksum to stdout.
func (a *App) SnapshotCat(ctx context.Context, checksum string) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	store, err := a.snapshotStore(cfg)
	if err != nil {
		return err
	}
	r, err := store.Cat(ctx, checksum)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	_, err = io.Copy(os.Stdout, r)
	return err
}

// SnapshotDelete removes a snapshot and reclaims unreferenced blobs.
func (a *App) SnapshotDelete(ctx context.Context, id string) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	store, err := a.snapshotStore(cfg)
	if err != nil {
		return err
	}
	if err := store.Delete(ctx, id); err != nil {
		return err
	}
	log.Success(fmt.Sprintf("snapshot deleted: %s", id))
	return nil
}

// SnapshotRestore writes the snapshot's version of the named files back into the
// home directory and removes files the snapshot recorded as absent. Files that
// already match the snapshot are skipped, and everything that will actually
// change is snapshotted first, so a restore is itself undoable. An empty files
// slice selects every entry of the snapshot.
func (a *App) SnapshotRestore(ctx context.Context, id string, files []string) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	store, err := a.snapshotStore(cfg)
	if err != nil {
		return err
	}

	entries, err := store.Files(id)
	if err != nil {
		return err
	}

	byPath := make(map[string]snapshot.File, len(entries))
	for _, f := range entries {
		byPath[filepath.Join(a.HomeDir, f.Path)] = f
	}

	// Resolve every target before writing anything, so a typo cannot leave a
	// half-restored home directory behind.
	var selected []snapshot.File
	var unknown []string
	if len(files) == 0 {
		selected = entries
	}
	seen := make(map[string]bool, len(files))
	for _, t := range files {
		abs := dotfile.HomePath(a.HomeDir, t)
		f, ok := byPath[abs]
		if !ok {
			unknown = append(unknown, t)
			continue
		}
		if !seen[abs] {
			seen[abs] = true
			selected = append(selected, f)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("no file(s) in snapshot %s: %s", id, strings.Join(unknown, ", "))
	}

	var pending []snapshot.File
	for _, f := range selected {
		abs := filepath.Join(a.HomeDir, f.Path)
		fi, err := os.Lstat(abs)
		if err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return fmt.Errorf("stat %s: %w", abs, err)
		}
		exists := err == nil
		if exists && fi.Mode()&os.ModeSymlink != 0 {
			// Writing would follow the link and clobber its target, and the
			// pre-restore backup skips symlinks, so nothing would be undoable.
			return fmt.Errorf("refusing to restore %s: it is a symlink in the home directory", f.Path)
		}
		if f.Absent {
			if !exists {
				log.Step(fmt.Sprintf("%s is already absent", f.Path))
				continue
			}
		} else if exists {
			current, err := hash.GetHash(abs)
			if err != nil {
				return fmt.Errorf("hash %s: %w", abs, err)
			}
			if current == f.Checksum {
				log.Step(fmt.Sprintf("%s is already at the snapshot version", f.Path))
				continue
			}
		}
		pending = append(pending, f)
	}

	if len(pending) == 0 {
		log.Info("nothing to restore; all files already match the snapshot")
		return nil
	}

	backup := make([]dotfile.Pair, 0, len(pending))
	for _, f := range pending {
		backup = append(backup, dotfile.Pair{Dst: filepath.Join(a.HomeDir, f.Path)})
	}
	meta, err := a.autoSnapshot(ctx, cfg, backup, "auto: before restore "+id)
	if err != nil {
		return fmt.Errorf("snapshot before restore: %w", err)
	}
	// Store.Create skips paths it cannot read; restoring over them would not be undoable.
	if meta.FileCount != len(pending) {
		return fmt.Errorf("snapshot before restore %s covers %d of %d file(s); nothing restored", meta.ID, meta.FileCount, len(pending))
	}

	if err := a.restoreEntries(ctx, store, id, pending); err != nil {
		return err
	}

	log.Success(fmt.Sprintf("Restored %d file(s).", len(pending)))
	return nil
}

// restoreEntries puts each manifest entry of snapshot id back into the home
// directory: absent entries are removed, all others are written from their
// blob. It neither resolves targets nor takes a backup; callers do both first.
func (a *App) restoreEntries(ctx context.Context, store *snapshot.Store, id string, entries []snapshot.File) error {
	for _, f := range entries {
		abs := filepath.Join(a.HomeDir, f.Path)
		if f.Absent {
			log.Step(fmt.Sprintf("removing %s (absent in snapshot)", f.Path))
			if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove %s: %w", f.Path, err)
			}
			continue
		}
		r, err := store.Cat(ctx, f.Checksum)
		if err != nil {
			return fmt.Errorf("read %s from snapshot: %w", f.Path, err)
		}
		werr := writeFile(abs, r, f.Mode)
		_ = r.Close()
		if werr != nil {
			return fmt.Errorf("restore %s: %w", f.Path, werr)
		}
		log.Step(fmt.Sprintf("%s --> %s", id, abs))
	}
	return nil
}
