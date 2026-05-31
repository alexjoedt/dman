package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexjoedt/dman/internal/dotfile"
	"github.com/alexjoedt/dman/internal/snapshot"
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

func (a *App) autoSnapshot(ctx context.Context, cfg *Config, pairs []dotfile.Pair) error {
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
	_, err = store.Create(ctx, a.HomeDir, existing, "auto: before apply")
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

	pairs, err := dotfile.CollectBase(filepath.Join(cfg.Path, "base"), a.HomeDir)
	if err != nil {
		return fmt.Errorf("collect base dotfiles: %w", err)
	}
	profileDir := filepath.Join(cfg.Path, "profiles", cfg.Profile)
	if isExist(profileDir) {
		pp, err := dotfile.CollectProfile(profileDir, a.HomeDir)
		if err != nil {
			return fmt.Errorf("collect profile dotfiles: %w", err)
		}
		pairs = append(pairs, pp...)
	}

	var existing []string
	for _, p := range dotfile.Merge(pairs) {
		if isExist(p.Dst) {
			existing = append(existing, p.Dst)
		}
	}
	if len(existing) == 0 {
		fmt.Println("no tracked dotfiles found on disk; nothing to snapshot")
		return nil
	}

	meta, err := store.Create(ctx, a.HomeDir, existing, message)
	if err != nil {
		return err
	}
	fmt.Printf("snapshot created: %s (%d file(s))\n", meta.ID, meta.FileCount)
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
	fmt.Printf("snapshot %s deleted\n", id)
	return nil
}
