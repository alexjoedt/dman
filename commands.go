package dman

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Init clones the remote dotfile repository and writes the config.
func (a *App) Init(ctx context.Context, repoURL, dest string) error {
	if repoURL == "" {
		return fmt.Errorf("repository URL is required")
	}
	if _, err := url.ParseRequestURI(repoURL); err != nil {
		return fmt.Errorf("invalid repository URL '%s': %w", repoURL, err)
	}

	if dest == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("get home directory: %w", err)
		}
		dest = filepath.Join(h, ".local", "share", "dman")
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
func (a *App) Apply(ctx context.Context, profileFlag string, dryRun bool, runScripts bool) error {
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

	if err := repo.Pull(ctx); err != nil {
		return fmt.Errorf("pull: %w", err)
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
	} else {
		fmt.Printf("Notice: no profile directory found for '%s', applying base only.\n", profile)
	}

	scriptCount := 0
	fileCount := 0

	for _, p := range mergePairs(pairs) {
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

		if dryRun {
			fmt.Printf("[dry-run] %s --> %s\n", p.src, p.dst)
			continue
		}

		if isExist(p.dst) {
			if err := backupFile(p.dst, a.HomeDir, a.BackupDir); err != nil {
				return fmt.Errorf("backup %s: %w", p.dst, err)
			}
		}

		if err := os.MkdirAll(filepath.Dir(p.dst), a.HomeMode); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(p.dst), err)
		}
		if err := copyFile(p.dst, p.src); err != nil {
			return fmt.Errorf("copy %s: %w", p.src, err)
		}
		fmt.Printf("%s --> %s\n", p.src, p.dst)
		fileCount++
	}

	if runScripts && !dryRun {
		n, err := runScriptDir(ctx, filepath.Join(cfg.Path, "base", "scripts"))
		if err != nil {
			return err
		}
		scriptCount += n

		if isExist(profileDir) {
			n, err = runScriptDir(ctx, filepath.Join(profileDir, "scripts"))
			if err != nil {
				return err
			}
			scriptCount += n
		}
	}

	if !dryRun {
		fmt.Printf("Applied %d file(s). Ran %d script(s).\n", fileCount, scriptCount)
	}
	return nil
}

// Add copies dotfiles from the home directory into the repository, commits, and pushes.
func (a *App) Add(ctx context.Context, files []string, profileFlag string) error {
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
	report := make(map[string]string)

	for _, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil {
			return fmt.Errorf("resolve path: %w", err)
		}

		rel, err := filepath.Rel(a.HomeDir, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("file is not under home directory: %s", abs)
		}

		base := filepath.Base(abs)
		if !strings.HasPrefix(base, ".") {
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
		} else if isExist(filepath.Join(cfg.Path, "profiles", profile, dotRel)) {
			dst = filepath.Join(cfg.Path, "profiles", profile, dotRel)
		} else {
			dst = filepath.Join(cfg.Path, "base", dotRel)
		}

		if isExist(dst) {
			report[abs] = "update"
		} else {
			report[abs] = "add"
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
	for f, action := range report {
		msgs = append(msgs, action+" "+filepath.Base(f))
	}
	if err := repo.Commit(ctx, strings.Join(msgs, ", ")); err != nil {
		return err
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
		if rel == "scripts" || strings.HasPrefix(rel, "scripts"+string(filepath.Separator)) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
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

// backupFile copies dst to BackupDir with an encoded backup filename.
func backupFile(dst, homeDir, backupDir string) error {
	name := backupName(dst, homeDir)
	backupPath := filepath.Join(backupDir, name)
	return copyFile(backupPath, dst)
}

// backupName generates the backup filename from the home-relative path.
// ~/.zshrc -> _zshrc_20260509_120000.bak
// ~/.config/nvim/init.lua -> _config_nvim_init.lua_20260509_120000.bak
func backupName(dst, homeDir string) string {
	rel, err := filepath.Rel(homeDir, dst)
	if err != nil {
		rel = filepath.Base(dst)
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for i, p := range parts {
		if i == 0 && strings.HasPrefix(p, ".") {
			parts[i] = "_" + p[1:]
		}
	}
	encoded := strings.Join(parts, "_")
	ts := time.Now().Format("20060102_150405")
	ext := filepath.Ext(encoded)
	base := strings.TrimSuffix(encoded, ext)
	return fmt.Sprintf("%s_%s%s.bak", base, ts, ext)
}

// runScriptDir runs all executable files in dir in lexicographic order.
func runScriptDir(ctx context.Context, dir string) (int, error) {
	if !isExist(dir) {
		return 0, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read script dir %s: %w", dir, err)
	}

	var scripts []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Mode()&0o111 == 0 {
			continue
		}
		scripts = append(scripts, filepath.Join(dir, e.Name()))
	}
	sort.Strings(scripts)

	for _, s := range scripts {
		fmt.Printf("Running script: %s\n", s)
		cmd := exec.CommandContext(ctx, s) //nolint:gosec // script paths come from the managed repo
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return 0, fmt.Errorf("script %s failed: %w", s, err)
		}
	}
	return len(scripts), nil
}
