package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
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

	whole := len(files) == 0
	var pending []snapshot.File
	skipped := 0
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
			if whole {
				log.Warn(fmt.Sprintf("skipping %s: it is a symlink in the home directory", f.Path))
				continue
			}
			return fmt.Errorf("refusing to restore %s: it is a symlink in the home directory", f.Path)
		}
		if f.Absent {
			if !exists {
				skipped++
				if !whole {
					log.Step(fmt.Sprintf("%s is already absent", f.Path))
				}
				continue
			}
		} else if exists {
			current, err := hash.GetHash(abs)
			if err != nil {
				return fmt.Errorf("hash %s: %w", abs, err)
			}
			if current == f.Checksum {
				skipped++
				if !whole {
					log.Step(fmt.Sprintf("%s is already at the snapshot version", f.Path))
				}
				continue
			}
		}
		pending = append(pending, f)
	}
	if whole && skipped > 0 {
		log.Info(fmt.Sprintf("%d file(s) already match the snapshot", skipped))
	}

	// Check every blob before writing anything, so a pruned store cannot leave
	// a half-restored home directory behind.
	for _, f := range pending {
		if f.Absent {
			continue
		}
		r, err := store.Cat(ctx, f.Checksum)
		if err != nil {
			return fmt.Errorf("snapshot %s is incomplete, nothing restored: %s: %w", id, f.Path, err)
		}
		_ = r.Close()
	}

	if len(pending) == 0 {
		log.Info("nothing to restore; all files already match the snapshot")
		return nil
	}

	targets := make([]string, len(pending))
	for i, f := range pending {
		targets[i] = filepath.Join(a.HomeDir, f.Path)
	}
	meta, err := store.Create(ctx, a.HomeDir, targets, "auto: before restore "+id)
	if err != nil {
		return fmt.Errorf("snapshot before restore: %w", err)
	}
	// Store.Create skips paths it cannot read; restoring over them would not be undoable.
	if meta.FileCount != len(pending) {
		return fmt.Errorf("snapshot before restore %s covers %d of %d file(s); nothing restored", meta.ID, meta.FileCount, len(pending))
	}

	err = a.writeAll(ctx, store, meta.ID, targets, func(i int) error {
		return a.restoreEntry(ctx, store, id, pending[i])
	})
	if err != nil {
		return err
	}

	log.Success(fmt.Sprintf("Restored %d file(s). Undo with: dman snapshot restore %s", len(pending), meta.ID))
	return nil
}

// restoreEntry removes f when it is recorded absent and writes it from its
// blob otherwise.
func (a *App) restoreEntry(ctx context.Context, store *snapshot.Store, id string, f snapshot.File) error {
	abs := filepath.Join(a.HomeDir, f.Path)
	if f.Absent {
		log.Step(fmt.Sprintf("removing %s (absent in snapshot)", f.Path))
		if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", f.Path, err)
		}
		return nil
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
	return nil
}

// writeAll calls write for each of the absolute home paths in targets, in
// order. SIGINT and SIGTERM cancel ctx for the duration of the call only, and
// a cancelled ctx counts as a failed write, checked between two writes. When a
// write fails, the targets already written are put back from snapshot backup
// in store; with a nil store they are left in place. A failed write leaves its
// own file untouched, but parent directories it created stay. A second signal
// kills the process.
func (a *App) writeAll(ctx context.Context, store *snapshot.Store, backup string, targets []string, write func(i int) error) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()
	for i := range targets {
		err := ctx.Err()
		if err == nil {
			err = write(i)
		}
		if err != nil {
			return a.rollBack(ctx, store, backup, targets[:i], err)
		}
	}
	return nil
}

// rollBack restores the written targets from snapshot backup and returns cause
// annotated with the outcome. It takes no snapshot of its own and ignores
// cancellation of ctx, which is often what made the write fail.
func (a *App) rollBack(ctx context.Context, store *snapshot.Store, backup string, written []string, cause error) error {
	if len(written) == 0 {
		if store != nil && errors.Is(cause, context.Canceled) {
			return fmt.Errorf("%w; nothing was written, snapshot %s was kept", cause, backup)
		}
		return cause
	}
	if store == nil {
		return fmt.Errorf("%w; %d file(s) written so far were left in place (snapshots off)", cause, len(written))
	}
	log.Warn(fmt.Sprintf("rolling back %d file(s) from snapshot %s", len(written), backup))
	if err := a.restorePaths(context.WithoutCancel(ctx), store, backup, written); err != nil {
		return errors.Join(cause, fmt.Errorf("roll back from snapshot %s incomplete, these file(s) remain modified:\n%w\nrestore manually with: dman snapshot restore %s", backup, err, backup))
	}
	return fmt.Errorf("%w; rolled back %d file(s) from snapshot %s", cause, len(written), backup)
}

// restorePaths restores the absolute home paths in targets from snapshot id.
// It is best effort: every target is attempted, and the returned error joins
// one error per target that could not be restored.
func (a *App) restorePaths(ctx context.Context, store *snapshot.Store, id string, targets []string) error {
	entries, err := store.Files(id)
	if err != nil {
		return err
	}
	byPath := make(map[string]snapshot.File, len(entries))
	for _, f := range entries {
		byPath[filepath.Join(a.HomeDir, f.Path)] = f
	}
	var errs []error
	for _, t := range targets {
		f, ok := byPath[t]
		if !ok {
			errs = append(errs, fmt.Errorf("%s is not in the snapshot", t))
			continue
		}
		if err := a.restoreEntry(ctx, store, id, f); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
