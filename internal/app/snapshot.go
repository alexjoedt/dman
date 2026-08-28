package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
// overwritten. Pairs whose destination does not exist yet are skipped: there is
// nothing to preserve.
func (a *App) autoSnapshot(ctx context.Context, cfg *Config, pairs []dotfile.Pair, message string) error {
	var existing []string
	for _, p := range pairs {
		if isExist(p.Dst) {
			existing = append(existing, p.Dst)
		}
	}
	if len(existing) == 0 {
		return nil
	}
	store, err := a.snapshotStore(cfg)
	if err != nil {
		return err
	}
	_, err = store.Create(ctx, a.HomeDir, existing, message)
	return err
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

	var existing []string
	for _, p := range dotfile.Merge(pairs) {
		if isExist(p.Dst) {
			existing = append(existing, p.Dst)
		}
	}
	if len(existing) == 0 {
		log.Warn("no tracked dotfiles found on disk; nothing to snapshot")
		return nil
	}

	meta, err := store.Create(ctx, a.HomeDir, existing, message)
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
		fmt.Printf("%-12s  %s\n", f.Checksum[:12], f.Path)
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
// home directory. Files whose current contents already match the snapshot are
// skipped, and everything that will actually change is snapshotted first, so a
// restore is itself undoable.
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
	for _, t := range files {
		f, ok := byPath[dotfile.HomePath(a.HomeDir, t)]
		if !ok {
			unknown = append(unknown, t)
			continue
		}
		selected = append(selected, f)
	}
	if len(unknown) > 0 {
		return fmt.Errorf("no file(s) in snapshot %s: %s", id, strings.Join(unknown, ", "))
	}

	var pending []snapshot.File
	for _, f := range selected {
		abs := filepath.Join(a.HomeDir, f.Path)
		if fi, err := os.Lstat(abs); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			// Writing would follow the link and clobber its target, and the
			// pre-restore backup skips symlinks, so nothing would be undoable.
			return fmt.Errorf("refusing to restore %s: it is a symlink in the home directory", f.Path)
		}
		if isExist(abs) {
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
	if err := a.autoSnapshot(ctx, cfg, backup, "auto: before restore "+id); err != nil {
		return fmt.Errorf("snapshot before restore: %w", err)
	}

	for _, f := range pending {
		abs := filepath.Join(a.HomeDir, f.Path)
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

	log.Success(fmt.Sprintf("Restored %d file(s).", len(pending)))
	return nil
}
