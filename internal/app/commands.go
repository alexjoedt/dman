package app

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
