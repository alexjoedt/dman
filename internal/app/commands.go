package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alexjoedt/dman/internal/crypt"
	"github.com/alexjoedt/dman/internal/dotfile"
	"github.com/alexjoedt/dman/internal/git"
	"github.com/alexjoedt/dman/internal/hash"
	"github.com/alexjoedt/dman/internal/profile"
	"github.com/alexjoedt/log"
	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
)

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

	dest, err := filepath.Abs(dest)
	if err != nil {
		return fmt.Errorf("resolve destination: %w", err)
	}

	if isExist(dest) {
		return fmt.Errorf("destination already exists: %s; remove it or use --destination", dest)
	}

	if err := git.Clone(ctx, log.Writer(log.DEBUG), repoURL, dest); err != nil {
		return fmt.Errorf("git clone '%s': %w", repoURL, err)
	}

	if !isDotfileRepo(dest) {
		removeClone(dest)
		return fmt.Errorf("repository has no dotfiles: expected dot_* entries or a profiles/ directory")
	}

	cfg := &Config{
		RepositoryURL: repoURL,
		Profile:       "default",
		Path:          dest,
	}
	if err := a.saveConfig(cfg); err != nil {
		removeClone(dest)
		return fmt.Errorf("save config: %w", err)
	}

	log.Success(fmt.Sprintf("Initialized dman. Repository: %s. Active profile: default", repoURL))
	return nil
}

// removeClone leaves nothing behind, otherwise the next init fails on
// "destination already exists" for a directory we created.
func removeClone(dest string) {
	if err := os.RemoveAll(dest); err != nil {
		log.Warn("could not remove cloned directory", "path", dest, "error", err)
	}
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

	name := profileFlag
	if name == "" {
		name = cfg.Profile
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

	pairs, err := a.collectTracked(cfg, name)
	if err != nil {
		return err
	}
	if profileFlag != "" && !profile.Exists(cfg.Path, name) {
		log.Warn("no profile directory found, applying base only", "profile", name)
	}

	merged := dotfile.Merge(pairs)

	if len(files) > 0 {
		merged, err = dotfile.FilterPairs(merged, a.HomeDir, files)
		if err != nil {
			return err
		}
	}

	merged = skipSymlinks(merged)

	codec, err := a.optionalCodec(cfg)
	if err != nil {
		return err
	}

	if !dryRun && !noSnapshot && cfg.Snapshots.Enabled {
		if err := a.autoSnapshot(ctx, cfg, merged, "auto: before apply"); err != nil {
			return fmt.Errorf("snapshot before apply: %w", err)
		}
	}

	fileCount := 0
	skippedNoKey := 0
	for _, p := range merged {
		// plain holds the decrypted content of an encrypted pair; it is
		// written directly instead of copying the ciphertext.
		var plain []byte
		var srcHash string
		if p.Encrypted {
			plain, err = readTracked(codec, p)
			if errors.Is(err, crypt.ErrNoKey) {
				skippedNoKey++
				continue
			}
			if err != nil {
				return err
			}
			srcHash = hash.Sum(plain)
		} else {
			srcHash, err = hash.GetHash(p.Src)
			if err != nil {
				return fmt.Errorf("hash %s: %w", p.Src, err)
			}
		}
		var dstHash string
		if isExist(p.Dst) {
			dstHash, err = hash.GetHash(p.Dst)
			if err != nil {
				return fmt.Errorf("hash %s: %w", p.Dst, err)
			}
		}
		changed := srcHash != dstHash
		// A decrypted secret must be private even when its content
		// already matches, e.g. right after add --encrypt on this machine.
		if !changed && p.Encrypted {
			if fi, err := os.Stat(p.Dst); err == nil && fi.Mode().Perm() != 0o600 {
				changed = true
			}
		}

		if !changed {
			continue
		}

		fileCount++

		if dryRun {
			log.Step(fmt.Sprintf("[dry-run] %s --> %s", p.Src, p.Dst))
			continue
		}

		if err := os.MkdirAll(filepath.Dir(p.Dst), a.HomeMode); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(p.Dst), err)
		}
		if p.Encrypted {
			// Encrypted means secret: the mode stored on the repo file
			// is irrelevant, the decrypted copy is always private.
			if err := writeFile(p.Dst, bytes.NewReader(plain), 0o600); err != nil {
				return fmt.Errorf("write %s: %w", p.Dst, err)
			}
		} else if err := copyFile(p.Dst, p.Src); err != nil {
			return fmt.Errorf("copy %s: %w", p.Src, err)
		}
		log.Step(fmt.Sprintf("%s --> %s", p.Src, p.Dst))
	}

	if dryRun {
		if fileCount == 0 {
			log.Info("[dry-run] all files up to date")
		}
		if skippedNoKey > 0 {
			log.Warn(fmt.Sprintf("[dry-run] %d encrypted file(s) skipped (no key configured)", skippedNoKey))
		}
		return nil
	}
	log.Success(fmt.Sprintf("Applied %d file(s).", fileCount))
	if skippedNoKey > 0 {
		log.Warn(fmt.Sprintf("%d encrypted file(s) skipped (no key configured)", skippedNoKey))
	}
	return nil
}

// skipSymlinks drops pairs whose repo or home path is itself a symlink,
// warning once per pair. dman stores files only; following a link would write
// through it to a target no snapshot covers. Only the leaf is checked: a
// symlinked parent directory (e.g. a stow-managed ~/.config) is followed.
func skipSymlinks(pairs []dotfile.Pair) []dotfile.Pair {
	kept := make([]dotfile.Pair, 0, len(pairs))
	for _, p := range pairs {
		switch {
		case isSymlink(p.Src):
			log.Warn("skip symlink in repository; dman no longer stores symlinks, replace it with a regular file or remove it", "file", p.Src)
		case isSymlink(p.Dst):
			log.Warn("skip symlink in home", "file", p.Dst)
		default:
			kept = append(kept, p)
		}
	}
	return kept
}

// SaveToRepo copies files from the home directory back into the repository
// (the reverse of Apply). Only tracked pairs whose Dst path is listed in
// targets are written; if targets is empty all tracked pairs are written.
// Git add/commit/push are performed when enabled in config, matching the
// behaviour of Add.
func (a *App) SaveToRepo(ctx context.Context, name string, targets []string) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}

	pairs, err := a.collectTracked(cfg, name)
	if err != nil {
		return err
	}

	merged := dotfile.Merge(pairs)

	if len(targets) > 0 {
		merged, err = dotfile.FilterPairs(merged, a.HomeDir, targets)
		if err != nil {
			return err
		}
	}

	gitOps := resolveAddGitOps(cfg, false, false, false)

	codec, err := a.optionalCodec(cfg)
	if err != nil {
		return err
	}

	var savedFiles []string
	for _, p := range skipSymlinks(merged) {
		if !isExist(p.Dst) {
			log.Warn("skip: home file does not exist", "file", p.Dst)
			continue
		}

		if p.Encrypted {
			// Re-encrypting unchanged plaintext would still produce new
			// ciphertext and a pointless commit.
			repoHash, err := plainHash(codec, p)
			if errors.Is(err, crypt.ErrNoKey) {
				log.Warn("skip encrypted file (no key configured)", "file", p.Src)
				continue
			}
			if err != nil {
				return err
			}
			homeHash, err := hash.GetHash(p.Dst)
			if err != nil {
				return fmt.Errorf("hash %s: %w", p.Dst, err)
			}
			if repoHash == homeHash {
				continue
			}
		}
		if err := storeInRepo(codec, p.Src, p.Dst, p.Encrypted); err != nil {
			return fmt.Errorf("copy %s: %w", p.Dst, err)
		}
		log.Step(fmt.Sprintf("%s --> %s", p.Dst, p.Src))
		savedFiles = append(savedFiles, p.Src)
	}

	if len(savedFiles) == 0 {
		log.Info("nothing saved")
		return nil
	}

	log.Success(fmt.Sprintf("Saved %d file(s) to repo.", len(savedFiles)))

	if !gitOps.add {
		return nil
	}

	repo, err := a.getRepo(cfg.Path)
	if err != nil {
		return err
	}
	if err := repo.Add(ctx, savedFiles...); err != nil {
		return err
	}
	if !gitOps.commit {
		return nil
	}

	rel := make([]string, 0, len(savedFiles))
	for _, f := range savedFiles {
		if r, err := filepath.Rel(cfg.Path, f); err == nil {
			rel = append(rel, r)
		} else {
			rel = append(rel, filepath.Base(f))
		}
	}
	commitMsg := "save: " + strings.Join(rel, ", ")
	if err := repo.Commit(ctx, commitMsg); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	if !gitOps.push {
		return nil
	}
	return repo.Push(ctx)
}

// Add copies dotfiles from the home directory into the repository. With
// encrypt the files are stored age-encrypted under the .crypt suffix; a file
// that is already stored encrypted stays encrypted regardless of the flag.
// Git add/commit/push steps are configurable and can be overridden via flags.
func (a *App) Add(ctx context.Context, files []string, profileFlag string, root, encrypt, addFlag, commitFlag, pushFlag bool) error {
	if len(files) == 0 {
		return fmt.Errorf("no files specified")
	}

	cfg, err := a.readConfig()
	if err != nil {
		return err
	}

	codec, err := a.optionalCodec(cfg)
	if err != nil {
		return err
	}

	gitOps := resolveAddGitOps(cfg, addFlag, commitFlag, pushFlag)
	profileFlag, err = addTarget(cfg, profileFlag, root)
	if err != nil {
		return err
	}

	var changedFiles []string
	var removedFiles []string

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
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to add %s: it is a symlink; add the file it points to instead", abs)
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
				if info.Mode()&os.ModeSymlink != 0 {
					log.Warn("skip symlink", "file", path)
					return nil
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
		executable, err := isExecutableFile(abs)
		if err != nil {
			return fmt.Errorf("inspect file %s: %w", abs, err)
		}
		if executable {
			log.Warn("skip executable", "file", abs)
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

		dst := filepath.Join(profile.Dir(cfg.Path, profileFlag), dotRel)

		dst, encrypted, stale, err := resolveRepoDst(dst, encrypt)
		if err != nil {
			return err
		}
		if isSymlink(dst) {
			log.Warn("skip: repository copy is a symlink; remove it from the repository first", "file", dst)
			continue
		}

		action := "add"
		if stale != "" {
			action = "encrypt"
		}
		if isExist(dst) {
			srcHash, err := hash.GetHash(abs)
			if err != nil {
				return fmt.Errorf("hash source %s: %w", abs, err)
			}
			dstHash, err := plainHash(codec, dotfile.Pair{Src: dst, Encrypted: encrypted})
			if errors.Is(err, crypt.ErrNoKey) {
				return fmt.Errorf("cannot update encrypted %s: %w", dst, err)
			}
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

		if err := storeInRepo(codec, dst, abs, encrypted); err != nil {
			return err
		}
		if stale != "" {
			if err := os.Remove(stale); err != nil {
				return fmt.Errorf("remove plain %s: %w", stale, err)
			}
			removedFiles = append(removedFiles, stale)
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
	if len(removedFiles) > 0 {
		if err := repo.Remove(ctx, removedFiles...); err != nil {
			return err
		}
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

	name := profileFlag
	if name == "" {
		name = cfg.Profile
	}

	pairs, err := a.collectTracked(cfg, name)
	if err != nil {
		return err
	}
	if profileFlag != "" && !profile.Exists(cfg.Path, name) {
		log.Warn("no profile directory found, syncing base only", "profile", name)
	}

	codec, err := a.optionalCodec(cfg)
	if err != nil {
		return err
	}

	merged := dotfile.Merge(pairs)
	gitOps := resolveAddGitOps(cfg, addFlag, commitFlag, pushFlag)

	var changed []string
	updated := 0
	for _, p := range skipSymlinks(merged) {
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
		repoHash, err := plainHash(codec, p)
		if errors.Is(err, crypt.ErrNoKey) {
			log.Warn("skip encrypted file (no key configured)", "file", p.Src)
			continue
		}
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
		if err := storeInRepo(codec, p.Src, p.Dst, p.Encrypted); err != nil {
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

// isDotfileRepo reports whether repoPath looks like a dman dotfile repository:
// it contains at least one top-level dot_* entry or a profiles/ directory.
func isDotfileRepo(repoPath string) bool {
	if isExist(profile.Root(repoPath)) {
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

// addTarget returns the profile that add writes to. An explicit profileFlag
// or root wins; otherwise a standalone active profile is the target, since it
// would never apply files stored in the repository root. An empty result means
// the repository root.
func addTarget(cfg *Config, profileFlag string, root bool) (string, error) {
	if root && profileFlag != "" {
		return "", errors.New("--root and --profile are mutually exclusive")
	}
	if root || profileFlag != "" || cfg.Profile == "" {
		return profileFlag, nil
	}
	standalone, err := profile.Standalone(cfg.Path, cfg.Profile)
	if err != nil {
		return "", err
	}
	if !standalone {
		return "", nil
	}
	log.Info(fmt.Sprintf("adding to profile %s (active standalone profile)", cfg.Profile))
	return cfg.Profile, nil
}

// collectTracked returns the apply pairs for a profile: the repository root as
// the base, overlaid by each layer of the profile's inheritance chain from the
// root-most ancestor down to the profile itself. A standalone chain keeps only
// the root pairs at or below its root include entries. A missing leaf directory is skipped; a missing parent or an inheritance
// cycle is an error.
func (a *App) collectTracked(cfg *Config, name string) ([]dotfile.Pair, error) {
	standalone, includes, err := profile.RootIncludes(cfg.Path, name)
	if err != nil {
		return nil, err
	}
	chain, err := profile.Chain(cfg.Path, name)
	if err != nil {
		return nil, err
	}
	var pairs []dotfile.Pair
	if !standalone || len(includes) > 0 {
		pairs, err = dotfile.Collect(cfg.Path, a.HomeDir, true)
		if err != nil {
			return nil, fmt.Errorf("collect base dotfiles: %w", err)
		}
		if standalone {
			pairs = a.filterRootIncludes(pairs, includes)
		}
	}
	for _, layer := range chain {
		dir := profile.Dir(cfg.Path, layer)
		if !isExist(dir) {
			continue
		}
		pp, err := dotfile.Collect(dir, a.HomeDir, false)
		if err != nil {
			return nil, fmt.Errorf("collect profile %q dotfiles: %w", layer, err)
		}
		pairs = append(pairs, pp...)
	}
	return pairs, nil
}

// filterRootIncludes keeps the pairs whose destination equals an include entry
// or lies below it.
func (a *App) filterRootIncludes(pairs []dotfile.Pair, includes []string) []dotfile.Pair {
	roots := make([]string, len(includes))
	for i, inc := range includes {
		roots[i] = dotfile.HomePath(a.HomeDir, inc)
	}
	var kept []dotfile.Pair
	for _, p := range pairs {
		for _, r := range roots {
			if rel, err := filepath.Rel(r, p.Dst); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				kept = append(kept, p)
				break
			}
		}
	}
	return kept
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
	return colorizeDiffANSI(diff)
}

// colorizeDiffANSI applies the diff colors unconditionally. The browse TUI
// calls it directly: it always renders to a terminal it controls.
func colorizeDiffANSI(diff string) string {
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

	name := profileFlag
	if name == "" {
		name = cfg.Profile
	}

	pairs, err := a.collectTracked(cfg, name)
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

	codec, err := a.optionalCodec(cfg)
	if err != nil {
		return err
	}

	changed := 0
	noKey := 0
	for _, p := range skipSymlinks(merged) {
		rel, err := filepath.Rel(a.HomeDir, p.Dst)
		if err != nil {
			rel = p.Dst
		}
		aLabel := filepath.Join("a", rel) // home (current)
		bLabel := filepath.Join("b", rel) // repo (incoming)

		srcContent, err := readTracked(codec, p)
		if errors.Is(err, crypt.ErrNoKey) {
			fmt.Printf("%s: encrypted, no key configured\n", bLabel)
			noKey++
			continue
		}
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
	if noKey > 0 {
		fmt.Printf("%d encrypted file(s) not compared (no key configured)\n", noKey)
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

// safeToRemove refuses repository paths that purge must never delete.
func safeToRemove(path, home string) error {
	if path == "" || path == "." || path == string(filepath.Separator) {
		return fmt.Errorf("refusing to purge unsafe repository path %q", path)
	}
	if home == "" || filepath.Clean(path) == filepath.Clean(home) || !isWithin(path, home) {
		return fmt.Errorf("refusing to purge %s: not a directory inside %s", path, home)
	}
	if !isDotfileRepo(path) {
		return fmt.Errorf("refusing to purge %s: not a dman repository", path)
	}
	return nil
}

// Purge removes all dman files after user confirmation.
func (a *App) Purge(ctx context.Context) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}

	removeRepo := true
	if err := safeToRemove(cfg.Path, a.HomeDir); err != nil {
		log.Warn(err.Error() + "; the repository is kept")
		removeRepo = false
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

	if removeRepo {
		if err := os.RemoveAll(cfg.Path); err != nil {
			return err
		}
	}
	log.Success("Purge complete")

	return nil
}
