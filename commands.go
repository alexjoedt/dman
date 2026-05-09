package dman

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
)

// ErrMigrationRequired is returned when the database contains dotfiles that
// were created with an older version of dman that stored content in BoltDB.
var ErrMigrationRequired = errors.New(
	`database was created with an older version of dman.
Run "dman migrate" to move dotfile content to the object store.`)

// Add copies dotfiles from the home directory into the repository, commits, and pushes.
func (a *App) Add(ctx context.Context, files []string) error {
	report := make(map[string]string)
	for _, f := range files {
		if err := addFile(a, f, report); err != nil {
			return err
		}
	}

	repo, err := getRepo(a.RepoDir)
	if err != nil {
		return err
	}

	for k := range report {
		fmt.Printf("%s: %s\n", report[k], k)
		file, err := transformPath(a.HomeDir, a.RepoDir, k)
		if err != nil {
			return err
		}
		if err = repo.Add(ctx, file); err != nil {
			return err
		}
		if err = repo.Commit(ctx, report[k]+" "+file); err != nil {
			return err
		}
	}

	return repo.Push(ctx)
}

// addFile copies a dotfile from src into the repo and records whether it was added or updated.
func addFile(app *App, src string, report map[string]string) error {
	dst, err := transformPath(app.HomeDir, app.RepoDir, src)
	if err != nil {
		return fmt.Errorf("add file: %w", err)
	}

	if isExist(dst) {
		report[src] = "update"
	} else {
		report[src] = "add"
	}

	if err := copyFile(dst, src); err != nil {
		return fmt.Errorf("add file: %w", err)
	}

	return nil
}

// Apply pulls from the remote and applies all dotfiles to the home directory.
func (a *App) Apply(ctx context.Context, dryRun bool) error {
	if !isExist(filepath.Join(a.ConfigDir, "config")) {
		return ErrNoConfig
	}

	repo, err := getRepo(a.RepoDir)
	if err != nil {
		return err
	}

	if err := repo.Pull(ctx); err != nil {
		return err
	}

	pairs, err := a.getDotfiles(repo.path)
	if err != nil {
		return fmt.Errorf("get dot files: %w", err)
	}

	if dryRun {
		preview, _ := applyFiles(pairs, a.HomeMode, true)
		printFileTable(preview)
		return nil
	}

	db, err := a.openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	if err := checkMigrationNeeded(db); err != nil {
		return err
	}

	if err = createSnapshot(ctx, db, a.Blobs, homePaths(pairs), "before-apply"); err != nil {
		return err
	}

	applied, err := applyFiles(pairs, a.HomeMode, false)
	if err != nil {
		return err
	}
	for _, p := range applied {
		fmt.Printf("%s --> %s\n", p.src, p.dst)
	}
	return nil
}

// getDotfiles scans the repo directory p for dotfiles and returns pairs mapping src→dst.
func (a *App) getDotfiles(p string) ([]filePair, error) {
	var pairs []filePair
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, fmt.Errorf("read dir '%s': %w", p, err)
	}

	for _, entry := range entries {
		if strings.Contains(entry.Name(), "dot_") {
			if err := a.update(p, entry, &pairs); err != nil {
				continue
			}
		}
	}

	return pairs, nil
}

func (a *App) update(p string, entry os.DirEntry, pairs *[]filePair) error {
	if entry.IsDir() {
		entries, err := os.ReadDir(filepath.Join(p, entry.Name()))
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := a.update(filepath.Join(p, entry.Name()), e, pairs); err != nil {
				return err
			}
		}
		return nil
	}

	src := filepath.Join(p, entry.Name())
	dst := strings.ReplaceAll(src, a.RepoDir, a.HomeDir)
	dst = strings.ReplaceAll(dst, "dot_", ".")

	*pairs = append(*pairs, filePair{src: src, dst: dst})
	return nil
}

// applyFiles applies or previews the given file pairs.
// In dry-run mode it returns only the pairs that would change without writing anything.
func applyFiles(pairs []filePair, homeMode os.FileMode, dryRun bool) ([]filePair, error) {
	var applied []filePair
	for _, p := range pairs {
		srcHash, err := getHash(p.src)
		if err != nil {
			return applied, fmt.Errorf("apply: hash %s: %w", p.src, err)
		}
		var dstHash string
		if isExist(p.dst) {
			dstHash, err = getHash(p.dst)
			if err != nil {
				return applied, fmt.Errorf("apply: hash %s: %w", p.dst, err)
			}
		}
		if srcHash == dstHash {
			continue
		}
		if !dryRun {
			if err := os.MkdirAll(filepath.Dir(p.dst), homeMode); err != nil {
				return applied, err
			}
			if err := copyFile(p.dst, p.src); err != nil {
				return applied, fmt.Errorf("apply: %w", err)
			}
		}
		applied = append(applied, p)
	}
	return applied, nil
}

// homePaths returns the destination (home) paths from a set of pairs.
func homePaths(pairs []filePair) []string {
	paths := make([]string, 0, len(pairs))
	for _, p := range pairs {
		paths = append(paths, p.dst)
	}
	return paths
}

// Backup creates a snapshot of the current dotfiles in the home directory.
func (a *App) Backup(ctx context.Context, tags []string) error {
	db, err := a.openDB()
	if err != nil {
		return fmt.Errorf("backup: open database: %w", err)
	}
	defer db.Close()

	config, err := a.readConfig()
	if err != nil {
		if errors.Is(err, ErrNoConfig) {
			return fmt.Errorf("no config: run dman init")
		}
		return fmt.Errorf("read config file: %w", err)
	}

	pairs, err := a.getDotfiles(config.Path)
	if err != nil {
		return err
	}

	if err := checkMigrationNeeded(db); err != nil {
		return err
	}

	return createSnapshot(ctx, db, a.Blobs, homePaths(pairs), tags...)
}

// Cat prints the dotfile with the given short ID to stdout.
func (a *App) Cat(ctx context.Context, id string) error {
	db, err := a.openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	if err := checkMigrationNeeded(db); err != nil {
		return err
	}

	if err := validateShortID(id); err != nil {
		return err
	}

	dotfile, err := getDotfileByID(db, id)
	if err != nil {
		return err
	}

	r, err := a.Blobs.Get(ctx, dotfile.Hash)
	if err != nil {
		return fmt.Errorf("get blob: %w", err)
	}
	defer r.Close()
	_, err = io.Copy(os.Stdout, r)
	return err
}

// Init clones the remote dotfile repository and writes the config.
func (a *App) Init(ctx context.Context, address, branch, dest string) error {
	if dest == "" {
		dest = a.RepoDir
	}

	if isExist(dest) {
		return fmt.Errorf("repository already exists (%s)", dest)
	}

	if isExist(a.DBPath) {
		return fmt.Errorf("dman is already initialized")
	}

	db, err := a.openDB()
	if err != nil {
		return fmt.Errorf("init db: %w", err)
	}
	defer db.Close()

	if address == "" {
		return errors.New("empty address for dotfile repository")
	}

	if _, err := url.Parse(address); err != nil {
		return fmt.Errorf("invalid address '%s': %w", address, err)
	}

	args := []string{address, dest}
	if branch != "" {
		args = []string{address, "--branch", branch, dest}
	}

	if err := cloneRepo(ctx, args...); err != nil {
		return fmt.Errorf("git clone '%s': %w", address, err)
	}

	repo, err := getRepo(dest)
	if err != nil {
		return err
	}

	b, err := repo.CurrentBranch(ctx)
	if err != nil {
		return fmt.Errorf("init repo: %w", err)
	}

	if err := a.saveConfig(&Config{Repository: address, Branch: b, Path: dest}); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	return nil
}

// List lists dotfiles. If all is true, all dotfiles are shown; otherwise lists by snapshotID.
func (a *App) List(ctx context.Context, snapshotID string, all bool) error {
	db, err := a.openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	var dotfiles []*Dotfile
	if all {
		dotfiles, err = listAllDotfiles(db)
		if err != nil {
			return err
		}
	} else {
		if len(snapshotID) < 12 {
			return fmt.Errorf("invalid id '%s'", snapshotID)
		}
		dotfiles, err = listDotfilesBySnapshot(db, []byte(snapshotID))
		if err != nil {
			return err
		}
	}

	printDotfileTable(dotfiles)
	return nil
}

// Pull pulls changes from the remote repository.
func (a *App) Pull(ctx context.Context) error {
	repo, err := getRepo(a.RepoDir)
	if err != nil {
		return err
	}
	return repo.Pull(ctx)
}

// Purge removes all dman files after user confirmation.
func (a *App) Purge(ctx context.Context) error {
	fmt.Print("Do you really want to purge all related files?? (y/N): ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))
	if input != "y" {
		fmt.Println("Purge aborted.")
		return nil
	}

	if err := os.RemoveAll(a.ConfigDir); err != nil {
		return err
	}
	fmt.Printf("\nRemoved %s\n", a.ConfigDir)

	if err := os.RemoveAll(a.RepoDir); err != nil {
		return err
	}
	fmt.Println("Removed", a.RepoDir)

	return nil
}

// Restore restores dotfiles from a specific snapshot to their home locations.
func (a *App) Restore(ctx context.Context, snapshotID, file string, dryRun bool) error {
	if err := validateShortID(snapshotID); err != nil {
		return fmt.Errorf("invalid snapshot ID: %w", err)
	}

	db, err := a.openDB()
	if err != nil {
		return fmt.Errorf("restore: open database: %w", err)
	}
	defer db.Close()

	if err := checkMigrationNeeded(db); err != nil {
		return err
	}

	dotfiles, err := listDotfilesBySnapshot(db, []byte(snapshotID))
	if err != nil {
		return fmt.Errorf("restore: get dotfiles from snapshot: %w", err)
	}

	if len(dotfiles) == 0 {
		return fmt.Errorf("no dotfiles found in snapshot %s", snapshotID[:12])
	}

	if file != "" {
		filtered := make([]*Dotfile, 0)
		for _, dotfile := range dotfiles {
			if filepath.Base(dotfile.Name) == file {
				filtered = append(filtered, dotfile)
			}
		}
		if len(filtered) == 0 {
			return fmt.Errorf("dotfile '%s' not found in snapshot %s", file, snapshotID[:12])
		}
		dotfiles = filtered
	}

	if dryRun {
		fmt.Printf("Would restore %d dotfiles from snapshot %s:\n\n", len(dotfiles), snapshotID[:12])
		for _, dotfile := range dotfiles {
			fmt.Printf("  %s\n", dotfile.Name)
		}
		return nil
	}

	config, err := a.readConfig()
	if err != nil {
		return fmt.Errorf("restore: read config: %w", err)
	}

	pairs, err := a.getDotfiles(config.Path)
	if err != nil {
		return fmt.Errorf("restore: get current dotfiles: %w", err)
	}

	if err = createSnapshot(ctx, db, a.Blobs, homePaths(pairs), "before-restore"); err != nil {
		return fmt.Errorf("restore: create backup snapshot: %w", err)
	}

	restored := 0
	for _, dotfile := range dotfiles {
		if err := a.restoreDotfile(ctx, dotfile); err != nil {
			fmt.Printf("Warning: failed to restore %s: %v\n", dotfile.Name, err)
			continue
		}
		fmt.Printf("Restored: %s\n", dotfile.Name)
		restored++
	}

	fmt.Printf("\nSuccessfully restored %d/%d dotfiles from snapshot %s\n", restored, len(dotfiles), snapshotID[:12])
	return nil
}

func (a *App) restoreDotfile(ctx context.Context, dotfile *Dotfile) error {
	dir := filepath.Dir(dotfile.Name)
	if err := os.MkdirAll(dir, a.HomeMode); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	r, err := a.Blobs.Get(ctx, dotfile.Hash)
	if err != nil {
		return fmt.Errorf("get blob for restore: %w", err)
	}
	defer r.Close()

	f, err := os.OpenFile(dotfile.Name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, a.HomeMode.Perm())
	if err != nil {
		return fmt.Errorf("open file for restore: %w", err)
	}
	defer f.Close()

	_, err = io.Copy(f, r)
	return err
}

// Snapshots lists all snapshots.
func (a *App) Snapshots(ctx context.Context) error {
	db, err := a.openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	snaps, err := listSnapshots(db)
	if err != nil {
		return err
	}

	printSnapshotTable(snaps)
	return nil
}

// EnvList lists all available environments (git branches).
func (a *App) EnvList(ctx context.Context) error {
	config, err := a.readConfig()
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	repo, err := getRepo(config.Path)
	if err != nil {
		return fmt.Errorf("failed to initialize repo: %w", err)
	}

	branches, currentBranch, err := repo.ListBranches(ctx)
	if err != nil {
		return fmt.Errorf("failed to list branches: %w", err)
	}

	fmt.Println("Available environments:")
	for _, branch := range branches {
		if branch == currentBranch {
			fmt.Printf("* %s (current)\n", branch)
		} else {
			fmt.Printf("  %s\n", branch)
		}
	}

	return nil
}

// EnvSwitch checks out a different environment (branch).
func (a *App) EnvSwitch(ctx context.Context, envName string) error {
	config, err := a.readConfig()
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	repo, err := getRepo(config.Path)
	if err != nil {
		return fmt.Errorf("failed to initialize repo: %w", err)
	}

	branches, _, err := repo.ListBranches(ctx)
	if err != nil {
		return fmt.Errorf("failed to list branches: %w", err)
	}

	branchExists := false
	for _, branch := range branches {
		if branch == envName {
			branchExists = true
			break
		}
	}

	if !branchExists {
		return fmt.Errorf("environment '%s' does not exist. Use 'dman env create %s' to create it", envName, envName)
	}

	if err := repo.Checkout(ctx, envName); err != nil {
		return fmt.Errorf("failed to switch to environment '%s': %w", envName, err)
	}

	config.Branch = envName
	if err := a.saveConfig(config); err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	fmt.Printf("Switched to environment: %s\n", envName)
	return nil
}

// EnvCreate creates a new environment (branch) and pushes it to the remote.
func (a *App) EnvCreate(ctx context.Context, envName string) error {
	config, err := a.readConfig()
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	repo, err := getRepo(config.Path)
	if err != nil {
		return fmt.Errorf("failed to initialize repo: %w", err)
	}

	branches, _, err := repo.ListBranches(ctx)
	if err != nil {
		return fmt.Errorf("failed to list branches: %w", err)
	}

	for _, branch := range branches {
		if branch == envName {
			return fmt.Errorf("environment '%s' already exists. Use 'dman env switch %s' to switch to it", envName, envName)
		}
	}

	if err := repo.CheckoutNewBranch(ctx, envName); err != nil {
		return fmt.Errorf("failed to create environment '%s': %w", envName, err)
	}

	if err := repo.PushNewBranch(ctx, envName); err != nil {
		return fmt.Errorf("failed to push new branch '%s': %w", envName, err)
	}

	config.Branch = envName
	if err := a.saveConfig(config); err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	fmt.Printf("Created and switched to new environment: %s\n", envName)
	return nil
}

// EnvCurrent prints the current environment (branch).
func (a *App) EnvCurrent(ctx context.Context) error {
	config, err := a.readConfig()
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	repo, err := getRepo(config.Path)
	if err != nil {
		return fmt.Errorf("failed to initialize repo: %w", err)
	}

	currentBranch, err := repo.CurrentBranch(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current branch: %w", err)
	}

	fmt.Printf("Current environment: %s\n", currentBranch)
	return nil
}

// Migrate moves dotfile content from BoltDB into the object store.
// It is idempotent: if interrupted, re-running picks up where it left off.
func (a *App) Migrate(ctx context.Context) error {
	db, err := a.openDB()
	if err != nil {
		return fmt.Errorf("migrate: open database: %w", err)
	}
	defer db.Close()

	dotfiles, err := listAllLegacyDotfiles(db)
	if err != nil {
		return fmt.Errorf("migrate: list dotfiles: %w", err)
	}

	var migrated int
	for _, df := range dotfiles {
		if df.Hash != "" || len(df.Data) == 0 {
			continue // already migrated or empty
		}

		h := sha256.Sum256(df.Data)
		hash := hex.EncodeToString(h[:])

		if err := a.Blobs.Put(ctx, hash, bytes.NewReader(df.Data)); err != nil {
			return fmt.Errorf("migrate: write blob for %s: %w", df.Name, err)
		}

		if err := setDotfileHash(db, df.ID, hash); err != nil {
			return fmt.Errorf("migrate: update dotfile record for %s: %w", df.Name, err)
		}

		migrated++
	}

	fmt.Printf("Migrated %d dotfiles\n", migrated)
	return nil
}

// GC removes blobs that are no longer referenced by any dotfile in the DB.
func (a *App) GC(ctx context.Context) error {
	db, err := a.openDB()
	if err != nil {
		return fmt.Errorf("gc: open database: %w", err)
	}
	defer db.Close()

	dotfiles, err := listAllDotfiles(db)
	if err != nil {
		return fmt.Errorf("gc: list dotfiles: %w", err)
	}

	referenced := make(map[string]struct{}, len(dotfiles))
	for _, df := range dotfiles {
		if df.Hash != "" {
			referenced[df.Hash] = struct{}{}
		}
	}

	var removed int
	result := a.Blobs.List(ctx, "")
	defer result.Close() //nolint:errcheck
	for result.Next() {
		key := result.Key()
		if _, ok := referenced[key]; !ok {
			if delErr := a.Blobs.Delete(ctx, key); delErr != nil {
				return fmt.Errorf("gc: delete blob %s: %w", key, delErr)
			}
			removed++
		}
	}
	if err := result.Err(); err != nil {
		return fmt.Errorf("gc: walk blobs: %w", err)
	}

	fmt.Printf("Removed %d unreferenced blobs\n", removed)
	return nil
}

func printDotfileTable(dotfiles []*Dotfile) {
	w := tabwriter.NewWriter(os.Stdout, 10, 0, 2, ' ', 0)
	fmt.Fprintf(w, "ID\tNAME\tCREATED AT\n")
	fmt.Fprintf(w, "--\t------\t----------\n")

	slices.SortFunc(dotfiles, func(a *Dotfile, b *Dotfile) int {
		if a.CreatedAt.After(b.CreatedAt.Time) {
			return 1
		} else if b.CreatedAt.After(a.CreatedAt.Time) {
			return -1
		}
		return 0
	})
	for _, d := range dotfiles {
		fmt.Fprintf(w, "%s\t%s\t%s\n", string(d.ID)[:12], d.Name, d.CreatedAt.String())
	}

	w.Flush()
}

func printSnapshotTable(snapshots []*Snapshot) {
	w := tabwriter.NewWriter(os.Stdout, 10, 0, 2, ' ', 0)
	fmt.Fprintf(w, "ID\tDATE\tTAGS\n")
	fmt.Fprintf(w, "--\t----\t----\n")

	for _, s := range snapshots {
		fmt.Fprintf(w, "%s\t%s\t%v\n", string(s.ID)[:12], s.Date.String(), s.Tags)
	}
	w.Flush()
}

func printFileTable(pairs []filePair) {
	w := tabwriter.NewWriter(os.Stdout, 10, 0, 2, ' ', 0)
	fmt.Fprintf(w, "DOTFILES\tHOME\n")
	fmt.Fprintf(w, "--------\t----\n")

	for _, p := range pairs {
		fmt.Fprintf(w, "%s\t%s\n", p.src, p.dst)
	}

	w.Flush()
}
