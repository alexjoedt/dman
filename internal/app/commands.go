package app

import (
	"bufio"
	"bytes"
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
	"github.com/alexjoedt/log"
	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
)

var runShell = func(ctx context.Context, shell, dir string) error {
	cmd := exec.CommandContext(ctx, shell)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// getRepo returns a git.Repo for the given path with its stderr writer
// configured to surface output only when verbose/debug logging is active.
func (a *App) getRepo(path string) (*git.Repo, error) {
	r, err := git.GetRepo(path)
	if err != nil {
		return nil, err
	}
	r.Stderr = log.Writer(log.DEBUG)
	return r, nil
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

	if err := git.Clone(ctx, log.Writer(log.DEBUG), repoURL, dest); err != nil {
		return fmt.Errorf("git clone '%s': %w", repoURL, err)
	}

	if !isDotfileRepo(dest) {
		return fmt.Errorf("repository has no dotfiles: expected dot_* entries or a profiles/ directory")
	}

	cfg := &Config{
		RepositoryURL: repoURL,
		Profile:       "default",
		Path:          dest,
	}
	if err := a.saveConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	log.Success(fmt.Sprintf("Initialized dman. Repository: %s. Active profile: default", repoURL))
	return nil
}

// Apply pulls from remote and applies dotfiles from the repository root and the
// active profile overlay to home. When noPull is true the git pull is skipped.
// When noSnapshot is true the automatic pre-apply snapshot is skipped even if
// enabled in config.
func (a *App) Apply(ctx context.Context, profileFlag string, dryRun, noPull, noSnapshot bool, files []string) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}

	profile := profileFlag
	if profile == "" {
		profile = cfg.Profile
	}

	repo, err := a.getRepo(cfg.Path)
	if err != nil {
		return err
	}

	if !noPull {
		if err := repo.Pull(ctx); err != nil {
			return fmt.Errorf("pull: %w", err)
		}
	}

	pairs, err := a.collectTracked(cfg, profile)
	if err != nil {
		return err
	}
	if profileFlag != "" && !isExist(filepath.Join(cfg.Path, "profiles", profile)) {
		log.Warn("no profile directory found, applying base only", "profile", profile)
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
		srcFi, err := os.Lstat(p.Src)
		if err != nil {
			return fmt.Errorf("stat %s: %w", p.Src, err)
		}
		srcIsSymlink := srcFi.Mode()&os.ModeSymlink != 0

		changed := true
		if srcIsSymlink {
			srcTarget, err := os.Readlink(p.Src)
			if err != nil {
				return fmt.Errorf("readlink %s: %w", p.Src, err)
			}
			dstFi, err := os.Lstat(p.Dst)
			if err == nil && dstFi.Mode()&os.ModeSymlink != 0 {
				dstTarget, err := os.Readlink(p.Dst)
				if err != nil {
					return fmt.Errorf("readlink %s: %w", p.Dst, err)
				}
				changed = srcTarget != dstTarget
			}
		} else {
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
			changed = srcHash != dstHash
		}

		if !changed {
			continue
		}

		fileCount++

		if dryRun {
			log.Step(fmt.Sprintf("[dry-run] %s --> %s", p.Src, p.Dst))
			continue
		}

		if srcIsSymlink {
			if err := copySymlink(p.Dst, p.Src); err != nil {
				return fmt.Errorf("copy symlink %s: %w", p.Src, err)
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(p.Dst), a.HomeMode); err != nil {
				return fmt.Errorf("mkdir %s: %w", filepath.Dir(p.Dst), err)
			}
			if err := copyFile(p.Dst, p.Src); err != nil {
				return fmt.Errorf("copy %s: %w", p.Src, err)
			}
		}
		log.Step(fmt.Sprintf("%s --> %s", p.Src, p.Dst))
	}

	if dryRun {
		if fileCount == 0 {
			log.Info("[dry-run] all files up to date")
		}
		return nil
	}
	log.Success(fmt.Sprintf("Applied %d file(s).", fileCount))
	return nil
}

// Add copies dotfiles from the home directory into the repository.
// Git add/commit/push steps are configurable and can be overridden via flags.
func (a *App) Add(ctx context.Context, files []string, profileFlag string, addFlag, commitFlag, pushFlag bool) error {
	if len(files) == 0 {
		return fmt.Errorf("no files specified")
	}

	cfg, err := a.readConfig()
	if err != nil {
		return err
	}

	profile := profileFlag
	gitOps := resolveAddGitOps(cfg, addFlag, commitFlag, pushFlag)

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
		fi, err := os.Lstat(abs)
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
		fi, err := os.Lstat(abs)
		if err != nil {
			return fmt.Errorf("stat %s: %w", abs, err)
		}
		isSymlink := fi.Mode()&os.ModeSymlink != 0

		if isSymlink {
			if !cfg.AddSymlinks {
				log.Warn("skip symlink (addSymlinks is false)", "file", abs)
				continue
			}
		} else {
			executable, err := isExecutableFile(abs)
			if err != nil {
				return fmt.Errorf("inspect file %s: %w", abs, err)
			}
			if executable {
				log.Warn("skip executable", "file", abs)
				continue
			}
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

		dst := filepath.Join(repoDestRoot(cfg.Path, profile), dotRel)

		action := "add"
		if isSymlink {
			srcTarget, err := os.Readlink(abs)
			if err != nil {
				return fmt.Errorf("readlink %s: %w", abs, err)
			}
			dstFi, err := os.Lstat(dst)
			if err == nil && dstFi.Mode()&os.ModeSymlink != 0 {
				dstTarget, err := os.Readlink(dst)
				if err != nil {
					return fmt.Errorf("readlink %s: %w", dst, err)
				}
				if srcTarget == dstTarget {
					continue
				}
				action = "update"
			}
		} else if isExist(dst) {
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

		if isSymlink {
			if err := copySymlink(dst, abs); err != nil {
				return fmt.Errorf("copy symlink: %w", err)
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return fmt.Errorf("mkdir: %w", err)
			}
			if err := copyFile(dst, abs); err != nil {
				return fmt.Errorf("copy file: %w", err)
			}
		}
		log.Step(fmt.Sprintf("%s: %s", action, abs))
		changedFiles = append(changedFiles, dst)
	}

	if len(changedFiles) == 0 {
		log.Info("nothing changed")
		return nil
	}

	if !gitOps.add {
		return nil
	}

	repo, err := a.getRepo(cfg.Path)
	if err != nil {
		return err
	}

	if err := repo.Add(ctx, changedFiles...); err != nil {
		return err
	}
	if !gitOps.commit {
		return nil
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
	if !gitOps.push {
		return nil
	}
	return repo.Push(ctx)
}

// AddSync synchronizes a dotfile directory into the repository and prunes
// files that no longer exist in the source directory.
func (a *App) AddSync(ctx context.Context, srcDir, profileFlag string, dryRun, addFlag, commitFlag, pushFlag bool) error {
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
	gitOps := resolveAddGitOps(cfg, addFlag, commitFlag, pushFlag)

	dotEncodedRoot, err := dotfile.TransformPath(a.HomeDir, cfg.Path, absSrcDir)
	if err != nil {
		return fmt.Errorf("transform sync directory: %w", err)
	}
	dotRelRoot := strings.TrimPrefix(dotEncodedRoot, cfg.Path+string(filepath.Separator))

	destRoot := repoDestRoot(cfg.Path, profile)
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

		isSymlink := info.Mode()&os.ModeSymlink != 0
		if isSymlink {
			if !cfg.AddSymlinks {
				log.Warn("skip symlink (addSymlinks is false)", "file", path)
				return nil
			}
		} else {
			executable, err := isExecutableFile(path)
			if err != nil {
				return fmt.Errorf("inspect file %s: %w", path, err)
			}
			if executable {
				log.Warn("skip executable", "file", path)
				return nil
			}
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
		srcFi, err := os.Lstat(src)
		if err != nil {
			return fmt.Errorf("stat %s: %w", src, err)
		}
		srcIsSymlink := srcFi.Mode()&os.ModeSymlink != 0

		if _, ok := currentFiles[dst]; ok {
			changed := true
			if srcIsSymlink {
				srcTarget, err := os.Readlink(src)
				if err != nil {
					return fmt.Errorf("readlink %s: %w", src, err)
				}
				dstFi, err := os.Lstat(dst)
				if err == nil && dstFi.Mode()&os.ModeSymlink != 0 {
					dstTarget, err := os.Readlink(dst)
					if err != nil {
						return fmt.Errorf("readlink %s: %w", dst, err)
					}
					changed = srcTarget != dstTarget
				}
			} else {
				srcHash, err := hash.GetHash(src)
				if err != nil {
					return fmt.Errorf("hash source %s: %w", src, err)
				}
				dstHash, err := hash.GetHash(dst)
				if err != nil {
					return fmt.Errorf("hash destination %s: %w", dst, err)
				}
				changed = srcHash != dstHash
			}
			if !changed {
				continue
			}
			updatedCount++
			if dryRun {
				log.Step(fmt.Sprintf("[dry-run] update: %s -> %s", src, dst))
			} else {
				if srcIsSymlink {
					if err := copySymlink(dst, src); err != nil {
						return fmt.Errorf("copy symlink: %w", err)
					}
				} else {
					if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
						return fmt.Errorf("mkdir: %w", err)
					}
					if err := copyFile(dst, src); err != nil {
						return fmt.Errorf("copy file: %w", err)
					}
				}
				log.Step("update: " + src)
			}
			addOrUpdate = append(addOrUpdate, dst)
			continue
		}

		addedCount++
		if dryRun {
			log.Step(fmt.Sprintf("[dry-run] add: %s -> %s", src, dst))
		} else {
			if srcIsSymlink {
				if err := copySymlink(dst, src); err != nil {
					return fmt.Errorf("copy symlink: %w", err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					return fmt.Errorf("mkdir: %w", err)
				}
				if err := copyFile(dst, src); err != nil {
					return fmt.Errorf("copy file: %w", err)
				}
			}
			log.Step("add: " + src)
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
			log.Info("[dry-run] nothing changed")
		} else {
			log.Info("nothing changed")
		}
		return nil
	}

	for _, p := range toDelete {
		if dryRun {
			log.Step("[dry-run] delete: " + p)
			continue
		}
		log.Step("delete: " + p)
	}

	if dryRun {
		return nil
	}
	if !gitOps.add {
		return nil
	}

	repo, err := a.getRepo(cfg.Path)
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
	if !gitOps.commit {
		return nil
	}

	deletedCount := len(toDelete)
	commitMsg := fmt.Sprintf("sync %s (+%d ~%d -%d)", relSrcToHome, addedCount, updatedCount, deletedCount)
	if err := repo.Commit(ctx, commitMsg); err != nil {
		return fmt.Errorf("commit %q: %w", commitMsg, err)
	}
	if !gitOps.push {
		return nil
	}
	return repo.Push(ctx)
}

// Sync copies the current home versions of all tracked dotfiles back into the
// repository (home -> repo), honoring the active profile overlay. It is the
// inverse of Apply: the tracked set is defined entirely by the repository, so
// only files that already exist in the repo are updated. Sync never deletes;
// tracked files that are missing from home are skipped with a warning. When a
// file is tracked in both the base and the active profile, only the profile
// copy (the one that wins on apply) is updated.
func (a *App) Sync(ctx context.Context, profileFlag string, dryRun, addFlag, commitFlag, pushFlag bool) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}

	profile := profileFlag
	if profile == "" {
		profile = cfg.Profile
	}

	pairs, err := a.collectTracked(cfg, profile)
	if err != nil {
		return err
	}
	if profileFlag != "" && !isExist(filepath.Join(cfg.Path, "profiles", profile)) {
		log.Warn("no profile directory found, syncing base only", "profile", profile)
	}

	merged := dotfile.Merge(pairs)
	gitOps := resolveAddGitOps(cfg, addFlag, commitFlag, pushFlag)

	var changed []string
	updated := 0
	for _, p := range merged {
		if !isExist(p.Dst) {
			log.Warn("skip missing home file", "file", p.Dst)
			continue
		}

		executable, err := isExecutableFile(p.Dst)
		if err != nil {
			return fmt.Errorf("inspect file %s: %w", p.Dst, err)
		}
		if executable {
			log.Warn("skip executable", "file", p.Dst)
			continue
		}

		homeHash, err := hash.GetHash(p.Dst)
		if err != nil {
			return fmt.Errorf("hash %s: %w", p.Dst, err)
		}
		repoHash, err := hash.GetHash(p.Src)
		if err != nil {
			return fmt.Errorf("hash %s: %w", p.Src, err)
		}
		if homeHash == repoHash {
			continue
		}

		updated++
		if dryRun {
			log.Step(fmt.Sprintf("[dry-run] update: %s --> %s", p.Dst, p.Src))
			continue
		}

		if err := os.MkdirAll(filepath.Dir(p.Src), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(p.Src), err)
		}
		if err := copyFile(p.Src, p.Dst); err != nil {
			return fmt.Errorf("copy %s: %w", p.Dst, err)
		}
		log.Step(fmt.Sprintf("update: %s --> %s", p.Dst, p.Src))
		changed = append(changed, p.Src)
	}

	if dryRun {
		if updated == 0 {
			log.Info("[dry-run] all tracked files up to date")
		}
		return nil
	}
	if len(changed) == 0 {
		log.Info("nothing changed")
		return nil
	}

	if !gitOps.add {
		return nil
	}

	repo, err := a.getRepo(cfg.Path)
	if err != nil {
		return err
	}

	sort.Strings(changed)
	toStage := make([]string, 0, len(changed))
	for _, abs := range changed {
		rel, err := filepath.Rel(cfg.Path, abs)
		if err != nil {
			return fmt.Errorf("stage path %s: %w", abs, err)
		}
		toStage = append(toStage, rel)
	}
	if err := repo.Add(ctx, toStage...); err != nil {
		return err
	}
	if !gitOps.commit {
		return nil
	}

	commitMsg := fmt.Sprintf("sync tracked files (~%d)", updated)
	if err := repo.Commit(ctx, commitMsg); err != nil {
		return fmt.Errorf("commit %q: %w", commitMsg, err)
	}
	if !gitOps.push {
		return nil
	}
	return repo.Push(ctx)
}

type addGitOps struct {
	add    bool
	commit bool
	push   bool
}

func resolveAddGitOps(cfg *Config, addFlag, commitFlag, pushFlag bool) addGitOps {
	ops := addGitOps{}
	if cfg != nil && cfg.Git != nil {
		ops.add = cfg.Git.AutoAdd
		ops.commit = cfg.Git.AutoCommit
		ops.push = cfg.Git.AutoPush
	}
	if pushFlag {
		ops.push = true
	}
	if commitFlag {
		ops.commit = true
	}
	if addFlag {
		ops.add = true
	}

	if ops.push {
		ops.commit = true
	}
	if ops.commit {
		ops.add = true
	}

	return ops
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

// repoDestRoot returns the directory inside the repository where files for the
// given profile are stored. An empty profile maps to the repository root (base);
// any named profile maps under profiles/<name>.
func repoDestRoot(repoPath, profile string) string {
	if profile == "" {
		return repoPath
	}
	return filepath.Join(repoPath, "profiles", profile)
}

// isDotfileRepo reports whether repoPath looks like a dman dotfile repository:
// it contains at least one top-level dot_* entry or a profiles/ directory.
func isDotfileRepo(repoPath string) bool {
	if isExist(filepath.Join(repoPath, "profiles")) {
		return true
	}
	entries, err := os.ReadDir(repoPath)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "dot_") {
			return true
		}
	}
	return false
}

// collectTracked returns the apply pairs for a profile: the repository root as
// the base, overlaid by profiles/<profile> when that directory exists.
func (a *App) collectTracked(cfg *Config, profile string) ([]dotfile.Pair, error) {
	pairs, err := dotfile.Collect(cfg.Path, a.HomeDir, true)
	if err != nil {
		return nil, fmt.Errorf("collect base dotfiles: %w", err)
	}
	profileDir := filepath.Join(cfg.Path, "profiles", profile)
	if isExist(profileDir) {
		pp, err := dotfile.Collect(profileDir, a.HomeDir, false)
		if err != nil {
			return nil, fmt.Errorf("collect profile dotfiles: %w", err)
		}
		pairs = append(pairs, pp...)
	}
	return pairs, nil
}

// diffColorEnabled reports whether ANSI color should be used for diff output.
// Color is disabled when the NO_COLOR env var is set or when stdout is not a
// terminal (e.g. piped to a file or another command).
func diffColorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// colorizeDiff adds ANSI color codes to a unified diff string when color is
// enabled. Added lines are green, removed lines red, hunk headers cyan, and
// file headers bold. Context lines and the summary are left unchanged.
func colorizeDiff(diff string) string {
	if !diffColorEnabled() {
		return diff
	}
	const (
		reset = "\033[0m"
		bold  = "\033[1m"
		red   = "\033[31m"
		green = "\033[32m"
		cyan  = "\033[36m"
	)
	var b strings.Builder
	for _, line := range strings.SplitAfter(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			b.WriteString(bold + line + reset)
		case strings.HasPrefix(line, "@@"):
			b.WriteString(cyan + line + reset)
		case strings.HasPrefix(line, "+"):
			b.WriteString(green + line + reset)
		case strings.HasPrefix(line, "-"):
			b.WriteString(red + line + reset)
		default:
			b.WriteString(line)
		}
	}
	return b.String()
}

// Diff prints a unified diff between the current home file (a/) and the
// incoming repo version (b/) for each tracked dotfile that differs. When files
// is non-empty only those dotfiles are compared, using the same resolution
// rules as Apply. Binary files that differ are noted without showing content.
func (a *App) Diff(_ context.Context, profileFlag string, files []string) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}

	profile := profileFlag
	if profile == "" {
		profile = cfg.Profile
	}

	pairs, err := a.collectTracked(cfg, profile)
	if err != nil {
		return err
	}

	merged := dotfile.Merge(pairs)

	if len(files) > 0 {
		merged, err = dotfile.FilterPairs(merged, a.HomeDir, files)
		if err != nil {
			return err
		}
	}

	changed := 0
	for _, p := range merged {
		srcContent, err := os.ReadFile(p.Src)
		if err != nil {
			return fmt.Errorf("read %s: %w", p.Src, err)
		}

		var dstContent []byte
		if isExist(p.Dst) {
			dstContent, err = os.ReadFile(p.Dst)
			if err != nil {
				return fmt.Errorf("read %s: %w", p.Dst, err)
			}
		}

		if bytes.Equal(srcContent, dstContent) {
			continue
		}

		rel, err := filepath.Rel(a.HomeDir, p.Dst)
		if err != nil {
			rel = p.Dst
		}
		aLabel := filepath.Join("a", rel) // home (current)
		bLabel := filepath.Join("b", rel) // repo (incoming)

		if bytes.Contains(srcContent, []byte{0}) || bytes.Contains(dstContent, []byte{0}) {
			fmt.Printf("Binary files %s and %s differ\n", aLabel, bLabel)
			changed++
			continue
		}

		edits := myers.ComputeEdits(span.URIFromPath(p.Src), string(dstContent), string(srcContent))
		unified := gotextdiff.ToUnified(aLabel, bLabel, string(dstContent), edits)
		fmt.Print(colorizeDiff(fmt.Sprint(unified)))
		changed++
	}

	if changed == 0 {
		fmt.Println("All tracked dotfiles are up to date.")
	} else {
		fmt.Printf("\n%d file(s) differ\n", changed)
	}
	return nil
}

// Pull pulls changes from the remote repository.
func (a *App) Pull(ctx context.Context) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	repo, err := a.getRepo(cfg.Path)
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
	repo, err := a.getRepo(cfg.Path)
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
		log.Warn("Purge aborted")
		return nil
	}

	if err := os.RemoveAll(a.ConfigDir); err != nil {
		return err
	}
	log.Step("Removed " + a.ConfigDir)

	if err := os.RemoveAll(cfg.Path); err != nil {
		return err
	}
	log.Success("Purge complete")

	return nil
}
