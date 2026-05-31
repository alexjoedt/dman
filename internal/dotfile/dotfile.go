package dotfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Pair is a source→destination file mapping.
type Pair struct {
	Src string
	Dst string
}

// TransformPath transforms a home-relative dotfile path to a repo path.
// ~/.zshrc -> <repo>/dot_zshrc
// ~/.config/nvim/init.lua -> <repo>/dot_config/nvim/init.lua
func TransformPath(home, repo string, p string) (string, error) {
	p = strings.TrimPrefix(p, home+string(filepath.Separator))
	if len(p) == 0 || p[0] != '.' {
		return "", fmt.Errorf("not a dotfile: %s", p)
	}
	p = strings.Replace(p, ".", "dot_", 1)
	return filepath.Join(repo, p), nil
}

// CollectBase walks baseDir recursively and returns Pairs mapping src→dst.
// Only top-level entries starting with dot_ are collected.
func CollectBase(baseDir, homeDir string) ([]Pair, error) {
	var pairs []Pair
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
		pairs = append(pairs, Pair{Src: path, Dst: dotToHome(homeDir, rel)})
		return nil
	})
	return pairs, err
}

// CollectProfile walks profileDir and returns Pairs for dot_-prefixed entries.
func CollectProfile(profileDir, homeDir string) ([]Pair, error) {
	var pairs []Pair
	err := filepath.Walk(profileDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(profileDir, path)
		if err != nil {
			return err
		}
		first := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		if !strings.HasPrefix(first, "dot_") {
			return nil
		}
		pairs = append(pairs, Pair{Src: path, Dst: dotToHome(homeDir, rel)})
		return nil
	})
	return pairs, err
}

// Merge deduplicates pairs so later entries (profile) win over earlier (base)
// for the same destination path.
func Merge(pairs []Pair) []Pair {
	seen := make(map[string]int)
	result := make([]Pair, 0, len(pairs))
	for _, p := range pairs {
		if idx, ok := seen[p.Dst]; ok {
			result[idx] = p
		} else {
			seen[p.Dst] = len(result)
			result = append(result, p)
		}
	}
	return result
}

// backupName returns a timestamped backup filename for dst.
// The name encodes the home-relative path with separators replaced by underscores,
// the original extension, and a UTC timestamp.
func backupName(dst, home string) string {
	rel, _ := filepath.Rel(home, dst)
	parts := strings.Split(rel, string(filepath.Separator))
	// Strip the leading dot from the first segment (.zshrc → zshrc).
	parts[0] = strings.TrimPrefix(parts[0], ".")
	flat := strings.Join(parts, "_")
	ext := filepath.Ext(flat)
	base := strings.TrimSuffix(flat, ext)
	ts := time.Now().UTC().Format("20060102_150405")
	return "_" + base + "_" + ts + ext + ".bak"
}

// dotToHome converts a dot_-encoded relative path back to a home-relative path.
// Only the first path segment is transformed.
func dotToHome(homeDir, rel string) string {
	parts := strings.SplitN(rel, string(filepath.Separator), 2)
	first := strings.Replace(parts[0], "dot_", ".", 1)
	if len(parts) == 1 {
		return filepath.Join(homeDir, first)
	}
	return filepath.Join(homeDir, first, parts[1])
}
