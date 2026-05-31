package dman

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Init clones the remote dotfile repository and writes the config.
func (a *App) Init(ctx context.Context, repoURL, dest string) error {
	if repoURL == "" {
		return fmt.Errorf("repository URL is required")
	}

	if dest == "" {
		dest = filepath.Join(a.HomeDir, ".local", "share", "dman")
	}

	if isExist(dest) {
		return fmt.Errorf("destination already exists: %s; remove it or use --destination", dest)
	}

	if err := cloneRepo(ctx, repoURL, dest); err != nil {
		return fmt.Errorf("git clone '%s': %w", repoURL, err)
	}

	if !isExist(filepath.Join(dest, "base")) {
		return fmt.Errorf("repository missing required base/ directory")
	}

	cfg := &Config{
		RepositoryURL: repoURL,
		Profile:       "default",
		Path:          dest,
	}
	if err := a.saveConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("Initialized dman. Repository: %s. Active profile: default\n", repoURL)
	return nil
}

// Apply pulls from remote and applies dotfiles from base + active profile to home.
// When noPull is true the git pull is skipped, allowing offline use.
// When noSnapshot is true the automatic pre-apply snapshot is skipped even if enabled in config.
func (a *App) Apply(ctx context.Context, profileFlag string, dryRun, noPull, noSnapshot bool) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}

	profile := profileFlag
	if profile == "" {
		profile = cfg.Profile
	}

	repo, err := getRepo(cfg.Path)
	if err != nil {
		return err
	}

	if !noPull {
		if err := repo.Pull(ctx); err != nil {
			return fmt.Errorf("pull: %w", err)
		}
	}

	// Collect base pairs
	pairs, err := collectDotfiles(filepath.Join(cfg.Path, "base"), a.HomeDir)
	if err != nil {
		return fmt.Errorf("collect base dotfiles: %w", err)
	}

	// Collect profile pairs (override base)
	profileDir := filepath.Join(cfg.Path, "profiles", profile)
	if isExist(profileDir) {
		profilePairs, err := collectProfileDotfiles(profileDir, a.HomeDir)
		if err != nil {
			return fmt.Errorf("collect profile dotfiles: %w", err)
		}
		pairs = append(pairs, profilePairs...)
	} else if profileFlag != "" {
		fmt.Printf("Notice: no profile directory found for '%s', applying base only.\n", profile)
	}

	merged := mergePairs(pairs)

	// Auto-snapshot: capture all tracked dotfiles that currently exist on disk
	// before making any changes. Abort the apply if the snapshot fails.
	if !dryRun && !noSnapshot && cfg.Snapshots.Enabled {
		if err := a.autoSnapshot(ctx, cfg, merged); err != nil {
			return fmt.Errorf("snapshot before apply: %w", err)
		}
	}

	fileCount := 0

	for _, p := range merged {
		srcHash, err := getHash(p.src)
		if err != nil {
			return fmt.Errorf("hash %s: %w", p.src, err)
		}
		var dstHash string
		if isExist(p.dst) {
			dstHash, err = getHash(p.dst)
			if err != nil {
				return fmt.Errorf("hash %s: %w", p.dst, err)
			}
		}
		if srcHash == dstHash {
			continue
		}

		fileCount++

		if dryRun {
			fmt.Printf("[dry-run] %s --> %s\n", p.src, p.dst)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(p.dst), a.HomeMode); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(p.dst), err)
		}
		if err := copyFile(p.dst, p.src); err != nil {
			return fmt.Errorf("copy %s: %w", p.src, err)
		}
		fmt.Printf("%s --> %s\n", p.src, p.dst)
	}

	if dryRun {
		if fileCount == 0 {
			fmt.Println("[dry-run] all files up to date.")
		}
		return nil
	}
	fmt.Printf("Applied %d file(s).\n", fileCount)
	return nil
}

// Add copies dotfiles from the home directory into the repository, commits, and pushes.
// When noPush is true the git push is skipped.
func (a *App) Add(ctx context.Context, files []string, profileFlag string, noPush bool) error {
	if len(files) == 0 {
		return fmt.Errorf("no files specified")
	}

	cfg, err := a.readConfig()
	if err != nil {
		return err
	}

	profile := profileFlag
	if profile == "" {
		profile = cfg.Profile
	}

	profileExplicit := profileFlag != ""

	var changedFiles []string
	// report maps abs file path -> action ("add" or "update")
	report := make(map[string]string)

	// Track original args for commit message: label (home-relative) and action.
	type origEntry struct {
		label  string // home-relative path, e.g. ".config/hypr"
		action string // "add" or "update"; "add" wins over "update"
	}
	origOrder := make([]string, 0, len(files)) // preserve input order (abs)
	origMap := make(map[string]*origEntry)      // abs of original arg -> entry
	fileOrigin := make(map[string]string)       // abs of expanded file -> abs of original arg

	// Expand any directories to their constituent files.
	var absFiles []string
	for _, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil {
			return fmt.Errorf("resolve path: %w", err)
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return fmt.Errorf("stat %s: %w", f, err)
		}
		rel, err := filepath.Rel(a.HomeDir, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("file is not under home directory: %s", abs)
		}
		if _, seen := origMap[abs]; !seen {
			origOrder = append(origOrder, abs)
			origMap[abs] = &origEntry{label: rel}
		}
		if fi.IsDir() {
			err = filepath.Walk(abs, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() && info.Name() == ".git" {
					return filepath.SkipDir
				}
				if !info.IsDir() {
					absFiles = append(absFiles, path)
					fileOrigin[path] = abs
				}
				return nil
			})
			if err != nil {
				return fmt.Errorf("walk %s: %w", f, err)
			}
		} else {
			absFiles = append(absFiles, abs)
			fileOrigin[abs] = abs
		}
	}

	for _, abs := range absFiles {
		rel, err := filepath.Rel(a.HomeDir, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("file is not under home directory: %s", abs)
		}

		topLevel := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		if !strings.HasPrefix(topLevel, ".") {
			return fmt.Errorf("not a dotfile: %s", abs)
		}

		dotEncoded, err := transformPath(a.HomeDir, cfg.Path, abs)
		if err != nil {
			return fmt.Errorf("transform path: %w", err)
		}

		dotRel := strings.TrimPrefix(dotEncoded, cfg.Path+string(filepath.Separator))

		var dst string
		if profileExplicit {
			dst = filepath.Join(cfg.Path, "profiles", profile, dotRel)
		} else if profile == "default" || profile == "base" {
			dst = filepath.Join(cfg.Path, "base", dotRel)
		} else {
			dst = filepath.Join(cfg.Path, "profiles", profile, dotRel)
		}

		if isExist(dst) {
			srcHash, err := getHash(abs)
			if err != nil {
				return fmt.Errorf("hash source %s: %w", abs, err)
			}
			dstHash, err := getHash(dst)
			if err != nil {
				return fmt.Errorf("hash destination %s: %w", dst, err)
			}
			if srcHash == dstHash {
				continue
			}
			report[abs] = "update"
		} else {
			report[abs] = "add"
		}

		// Propagate action to original entry ("add" takes priority).
		if orig, ok := origMap[fileOrigin[abs]]; ok {
			if orig.action != "add" {
				orig.action = report[abs]
			}
		}

		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("mkdir: %w", err)
		}
		if err := copyFile(dst, abs); err != nil {
			return fmt.Errorf("copy file: %w", err)
		}
		fmt.Printf("%s: %s\n", report[abs], abs)
		changedFiles = append(changedFiles, dst)
	}

	if len(changedFiles) == 0 {
		fmt.Println("nothing changed")
		return nil
	}

	repo, err := getRepo(cfg.Path)
	if err != nil {
		return err
	}

	if err := repo.Add(ctx, changedFiles...); err != nil {
		return err
	}

	var msgs []string
	for _, orig := range origOrder {
		e := origMap[orig]
		if e.action == "" {
			continue
		}
		msgs = append(msgs, e.action+" "+e.label)
	}
	commitMsg := strings.Join(msgs, ", ")
	if err := repo.Commit(ctx, commitMsg); err != nil {
		return fmt.Errorf("commit %q: %w", commitMsg, err)
	}
	if noPush {
		return nil
	}
	return repo.Push(ctx)
}

// Pull pulls changes from the remote repository.
func (a *App) Pull(ctx context.Context) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	repo, err := getRepo(cfg.Path)
	if err != nil {
		return err
	}
	return repo.Pull(ctx)
}

// Push pushes changes to the remote repository.
func (a *App) Push(ctx context.Context) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	repo, err := getRepo(cfg.Path)
	if err != nil {
		return err
	}
	return repo.Push(ctx)
}

// Purge removes all dman files after user confirmation.
func (a *App) Purge(ctx context.Context) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}

	fmt.Print("Do you really want to purge all related files? (y/N): ")
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
	fmt.Printf("Removed %s\n", a.ConfigDir)

	if err := os.RemoveAll(cfg.Path); err != nil {
		return err
	}
	fmt.Println("Removed", cfg.Path)

	return nil
}

// collectDotfiles walks baseDir recursively and returns filePairs mapping src->dst.
// Only top-level entries starting with dot_ are collected.
func collectDotfiles(baseDir, homeDir string) ([]filePair, error) {
	var pairs []filePair
	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(baseDir, path)
		if err != nil {
			return err
		}
		first := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		if !strings.HasPrefix(first, "dot_") {
			return nil
		}
		dst := dotToHome(homeDir, rel)
		pairs = append(pairs, filePair{src: path, dst: dst})
		return nil
	})
	return pairs, err
}

// collectProfileDotfiles walks profileDir, skipping scripts/ and non-dot_ entries.
func collectProfileDotfiles(profileDir, homeDir string) ([]filePair, error) {
	var pairs []filePair
	err := filepath.Walk(profileDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(profileDir, path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		first := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		if !strings.HasPrefix(first, "dot_") {
			return nil
		}
		dst := dotToHome(homeDir, rel)
		pairs = append(pairs, filePair{src: path, dst: dst})
		return nil
	})
	return pairs, err
}

// dotToHome converts a dot_-encoded relative path to a home path.
// Only the first path segment is transformed.
func dotToHome(homeDir, rel string) string {
	parts := strings.SplitN(rel, string(filepath.Separator), 2)
	first := strings.Replace(parts[0], "dot_", ".", 1)
	if len(parts) == 1 {
		return filepath.Join(homeDir, first)
	}
	return filepath.Join(homeDir, first, parts[1])
}

// mergePairs deduplicates pairs so later entries (profile) win over earlier (base)
// for the same destination path.
func mergePairs(pairs []filePair) []filePair {
	seen := make(map[string]int)
	result := make([]filePair, 0, len(pairs))
	for _, p := range pairs {
		if idx, ok := seen[p.dst]; ok {
			result[idx] = p
		} else {
			seen[p.dst] = len(result)
			result = append(result, p)
		}
	}
	return result
}

// autoSnapshot creates a snapshot of all tracked dotfile destinations that exist on disk.
func (a *App) autoSnapshot(ctx context.Context, cfg *Config, pairs []filePair) error {
	var existing []string
	for _, p := range pairs {
		if isExist(p.dst) {
			existing = append(existing, p.dst)
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

// snapshotStore returns a SnapshotStore for the given config.
// It returns an error when snapshots are not enabled.
func (a *App) snapshotStore(cfg *Config) (*SnapshotStore, error) {
	if !cfg.Snapshots.Enabled {
		return nil, fmt.Errorf("snapshots are not enabled; set snapshots.enabled=true in %s",
			filepath.Join(a.ConfigDir, configFileName))
	}
	dir := cfg.Snapshots.Path
	if dir == "" {
		dir = filepath.Join(a.HomeDir, ".local", "state", "dman", "snapshots")
	}
	return newSnapshotStore(dir)
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

	pairs, err := collectDotfiles(filepath.Join(cfg.Path, "base"), a.HomeDir)
	if err != nil {
		return fmt.Errorf("collect base dotfiles: %w", err)
	}
	profileDir := filepath.Join(cfg.Path, "profiles", cfg.Profile)
	if isExist(profileDir) {
		pp, err := collectProfileDotfiles(profileDir, a.HomeDir)
		if err != nil {
			return fmt.Errorf("collect profile dotfiles: %w", err)
		}
		pairs = append(pairs, pp...)
	}

	var existing []string
	for _, p := range mergePairs(pairs) {
		if isExist(p.dst) {
			existing = append(existing, p.dst)
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
	defer r.Close()
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



