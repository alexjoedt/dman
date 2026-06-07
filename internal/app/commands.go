package app

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alexjoedt/dman/internal/dotfile"
	"github.com/alexjoedt/dman/internal/git"
	"github.com/alexjoedt/dman/internal/hash"
)

var runShell = func(ctx context.Context, shell, dir string) error {
	cmd := exec.CommandContext(ctx, shell)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

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

	if err := git.Clone(ctx, repoURL, dest); err != nil {
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
// When noPull is true the git pull is skipped. When noSnapshot is true the
// automatic pre-apply snapshot is skipped even if enabled in config.
func (a *App) Apply(ctx context.Context, profileFlag string, dryRun, noPull, noSnapshot bool, files []string) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}

	profile := profileFlag
	if profile == "" {
		profile = cfg.Profile
	}

	repo, err := git.GetRepo(cfg.Path)
	if err != nil {
		return err
	}

	if !noPull {
		if err := repo.Pull(ctx); err != nil {
			return fmt.Errorf("pull: %w", err)
		}
	}

	pairs, err := dotfile.CollectBase(filepath.Join(cfg.Path, "base"), a.HomeDir)
	if err != nil {
		return fmt.Errorf("collect base dotfiles: %w", err)
	}

	profileDir := filepath.Join(cfg.Path, "profiles", profile)
	if isExist(profileDir) {
		pp, err := dotfile.CollectProfile(profileDir, a.HomeDir)
		if err != nil {
			return fmt.Errorf("collect profile dotfiles: %w", err)
		}
		pairs = append(pairs, pp...)
	} else if profileFlag != "" {
		fmt.Printf("Notice: no profile directory found for '%s', applying base only.\n", profile)
	}

	merged := dotfile.Merge(pairs)

	if len(files) > 0 {
		merged, err = dotfile.FilterPairs(merged, a.HomeDir, files)
		if err != nil {
			return err
		}
	}

	if !dryRun && !noSnapshot && cfg.Snapshots.Enabled {
		if err := a.autoSnapshot(ctx, cfg, merged); err != nil {
			return fmt.Errorf("snapshot before apply: %w", err)
		}
	}

	fileCount := 0
	for _, p := range merged {
		srcHash, err := hash.GetHash(p.Src)
		if err != nil {
			return fmt.Errorf("hash %s: %w", p.Src, err)
		}
		var dstHash string
		if isExist(p.Dst) {
			dstHash, err = hash.GetHash(p.Dst)
			if err != nil {
				return fmt.Errorf("hash %s: %w", p.Dst, err)
			}
		}
		if srcHash == dstHash {
			continue
		}

		fileCount++

		if dryRun {
			fmt.Printf("[dry-run] %s --> %s\n", p.Src, p.Dst)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(p.Dst), a.HomeMode); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(p.Dst), err)
		}
		if err := copyFile(p.Dst, p.Src); err != nil {
			return fmt.Errorf("copy %s: %w", p.Src, err)
		}
		fmt.Printf("%s --> %s\n", p.Src, p.Dst)
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

	type origEntry struct {
		label  string
		action string
	}
	origOrder := make([]string, 0, len(files))
	origMap := make(map[string]*origEntry)
	fileOrigin := make(map[string]string)

	var absFiles []string
	for _, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil {
			return fmt.Errorf("resolve path %s: %w", f, err)
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
		binary, err := isBinaryFile(abs)
		if err != nil {
			return fmt.Errorf("inspect file %s: %w", abs, err)
		}
		if binary {
			fmt.Printf("skip binary: %s\n", abs)
			continue
		}

		rel, err := filepath.Rel(a.HomeDir, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("file is not under home directory: %s", abs)
		}

		topLevel := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		if !strings.HasPrefix(topLevel, ".") {
			return fmt.Errorf("not a dotfile: %s", abs)
		}

		dotEncoded, err := dotfile.TransformPath(a.HomeDir, cfg.Path, abs)
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

		action := "add"
		if isExist(dst) {
			srcHash, err := hash.GetHash(abs)
			if err != nil {
				return fmt.Errorf("hash source %s: %w", abs, err)
			}
			dstHash, err := hash.GetHash(dst)
			if err != nil {
				return fmt.Errorf("hash destination %s: %w", dst, err)
			}
			if srcHash == dstHash {
				continue
			}
			action = "update"
		}

		if orig, ok := origMap[fileOrigin[abs]]; ok {
			if orig.action != "add" {
				orig.action = action
			}
		}

		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("mkdir: %w", err)
		}
		if err := copyFile(dst, abs); err != nil {
			return fmt.Errorf("copy file: %w", err)
		}
		fmt.Printf("%s: %s\n", action, abs)
		changedFiles = append(changedFiles, dst)
	}

	if len(changedFiles) == 0 {
		fmt.Println("nothing changed")
		return nil
	}

	repo, err := git.GetRepo(cfg.Path)
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

// AddSync synchronizes a dotfile directory into the repository and prunes
// files that no longer exist in the source directory.
func (a *App) AddSync(ctx context.Context, srcDir, profileFlag string, dryRun, noPush bool) error {
	if srcDir == "" {
		return fmt.Errorf("sync directory is required")
	}

	absSrcDir, err := filepath.Abs(srcDir)
	if err != nil {
		return fmt.Errorf("resolve sync directory %s: %w", srcDir, err)
	}
	fi, err := os.Stat(absSrcDir)
	if err != nil {
		return fmt.Errorf("stat %s: %w", srcDir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("sync path must be a directory: %s", srcDir)
	}
	if !isWithin(absSrcDir, a.HomeDir) {
		return fmt.Errorf("file is not under home directory: %s", absSrcDir)
	}

	relSrcToHome, err := filepath.Rel(a.HomeDir, absSrcDir)
	if err != nil || strings.HasPrefix(relSrcToHome, "..") {
		return fmt.Errorf("file is not under home directory: %s", absSrcDir)
	}
	topLevel := strings.SplitN(relSrcToHome, string(filepath.Separator), 2)[0]
	if !strings.HasPrefix(topLevel, ".") {
		return fmt.Errorf("not a dotfile directory: %s", absSrcDir)
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

	dotEncodedRoot, err := dotfile.TransformPath(a.HomeDir, cfg.Path, absSrcDir)
	if err != nil {
		return fmt.Errorf("transform sync directory: %w", err)
	}
	dotRelRoot := strings.TrimPrefix(dotEncodedRoot, cfg.Path+string(filepath.Separator))

	destRoot := filepath.Join(cfg.Path, "base")
	if profileExplicit || (profile != "default" && profile != "base") {
		destRoot = filepath.Join(cfg.Path, "profiles", profile)
	}
	syncScope := filepath.Join(destRoot, dotRelRoot)

	targetFiles := make(map[string]string)
	err = filepath.Walk(absSrcDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}
		if info.IsDir() {
			return nil
		}

		binary, err := isBinaryFile(path)
		if err != nil {
			return fmt.Errorf("inspect file %s: %w", path, err)
		}
		if binary {
			fmt.Printf("skip binary: %s\n", path)
			return nil
		}

		dotEncoded, err := dotfile.TransformPath(a.HomeDir, cfg.Path, path)
		if err != nil {
			return fmt.Errorf("transform path %s: %w", path, err)
		}
		dotRel := strings.TrimPrefix(dotEncoded, cfg.Path+string(filepath.Separator))
		dst := filepath.Join(destRoot, dotRel)
		if !isWithin(dst, syncScope) {
			return fmt.Errorf("computed destination outside sync scope: %s", dst)
		}
		targetFiles[dst] = path
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", srcDir, err)
	}

	currentFiles := make(map[string]struct{})
	if isExist(syncScope) {
		err = filepath.Walk(syncScope, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				return nil
			}
			if !isWithin(path, syncScope) {
				return fmt.Errorf("existing file outside sync scope: %s", path)
			}
			currentFiles[path] = struct{}{}
			return nil
		})
		if err != nil {
			return fmt.Errorf("walk sync scope %s: %w", syncScope, err)
		}
	}

	var addOrUpdate []string
	var toDelete []string
	addedCount := 0
	updatedCount := 0

	for dst, src := range targetFiles {
		if _, ok := currentFiles[dst]; ok {
			srcHash, err := hash.GetHash(src)
			if err != nil {
				return fmt.Errorf("hash source %s: %w", src, err)
			}
			dstHash, err := hash.GetHash(dst)
			if err != nil {
				return fmt.Errorf("hash destination %s: %w", dst, err)
			}
			if srcHash == dstHash {
				continue
			}
			updatedCount++
			if dryRun {
				fmt.Printf("[dry-run] update: %s -> %s\n", src, dst)
			} else {
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					return fmt.Errorf("mkdir: %w", err)
				}
				if err := copyFile(dst, src); err != nil {
					return fmt.Errorf("copy file: %w", err)
				}
				fmt.Printf("update: %s\n", src)
			}
			addOrUpdate = append(addOrUpdate, dst)
			continue
		}

		addedCount++
		if dryRun {
			fmt.Printf("[dry-run] add: %s -> %s\n", src, dst)
		} else {
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return fmt.Errorf("mkdir: %w", err)
			}
			if err := copyFile(dst, src); err != nil {
				return fmt.Errorf("copy file: %w", err)
			}
			fmt.Printf("add: %s\n", src)
		}
		addOrUpdate = append(addOrUpdate, dst)
	}

	for existing := range currentFiles {
		if _, ok := targetFiles[existing]; ok {
			continue
		}
		if !isWithin(existing, syncScope) {
			return fmt.Errorf("refusing to delete path outside sync scope: %s", existing)
		}
		toDelete = append(toDelete, existing)
	}

	sort.Strings(addOrUpdate)
	sort.Strings(toDelete)

	if len(addOrUpdate) == 0 && len(toDelete) == 0 {
		if dryRun {
			fmt.Println("[dry-run] nothing changed")
		} else {
			fmt.Println("nothing changed")
		}
		return nil
	}

	for _, p := range toDelete {
		if dryRun {
			fmt.Printf("[dry-run] delete: %s\n", p)
			continue
		}
		fmt.Printf("delete: %s\n", p)
	}

	if dryRun {
		return nil
	}

	repo, err := git.GetRepo(cfg.Path)
	if err != nil {
		return err
	}

	if len(addOrUpdate) > 0 {
		toStage := make([]string, 0, len(addOrUpdate))
		for _, abs := range addOrUpdate {
			rel, err := filepath.Rel(cfg.Path, abs)
			if err != nil {
				return fmt.Errorf("stage add path %s: %w", abs, err)
			}
			toStage = append(toStage, rel)
		}
		if err := repo.Add(ctx, toStage...); err != nil {
			return err
		}
	}

	if len(toDelete) > 0 {
		toRemove := make([]string, 0, len(toDelete))
		for _, abs := range toDelete {
			rel, err := filepath.Rel(cfg.Path, abs)
			if err != nil {
				return fmt.Errorf("stage remove path %s: %w", abs, err)
			}
			toRemove = append(toRemove, rel)
		}
		if err := repo.Remove(ctx, toRemove...); err != nil {
			return err
		}
	}

	deletedCount := len(toDelete)
	commitMsg := fmt.Sprintf("sync %s (+%d ~%d -%d)", relSrcToHome, addedCount, updatedCount, deletedCount)
	if err := repo.Commit(ctx, commitMsg); err != nil {
		return fmt.Errorf("commit %q: %w", commitMsg, err)
	}
	if noPush {
		return nil
	}
	return repo.Push(ctx)
}

func isWithin(path, base string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}

// Pull pulls changes from the remote repository.
func (a *App) Pull(ctx context.Context) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	repo, err := git.GetRepo(cfg.Path)
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
	repo, err := git.GetRepo(cfg.Path)
	if err != nil {
		return err
	}
	return repo.Push(ctx)
}

// Cd starts a shell in the local repository path.
func (a *App) Cd(ctx context.Context) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	if cfg.Path == "" {
		return fmt.Errorf("repository path is empty in config")
	}
	if !isExist(cfg.Path) {
		return fmt.Errorf("repository path does not exist: %s", cfg.Path)
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		return fmt.Errorf("SHELL is not set")
	}

	return runShell(ctx, shell, cfg.Path)
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
