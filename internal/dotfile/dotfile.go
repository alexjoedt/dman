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

// Collect walks dir recursively and returns Pairs mapping src→dst. Only files
// whose top-level path segment starts with dot_ are collected. The .git
// directory is always skipped. When skipProfiles is true, a top-level profiles
// directory is skipped as well; this is used when dir is the repository root so
// that profile overlays are not pulled into the base set.
func Collect(dir, homeDir string, skipProfiles bool) ([]Pair, error) {
	var pairs []Pair
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			if skipProfiles && info.Name() == "profiles" && filepath.Dir(path) == dir {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, path)
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

// FilterPairs returns only the pairs whose destination matches one of the
// given targets. Targets are interpreted as home-directory paths and may use
// a "~/" prefix or be bare dotfile names starting with ".". Returns an error
// if any target cannot be matched to a pair.
func FilterPairs(pairs []Pair, homeDir string, targets []string) ([]Pair, error) {
	normalize := func(t string) string {
		if strings.HasPrefix(t, "~/") {
			return filepath.Join(homeDir, t[2:])
		}
		if filepath.IsAbs(t) {
			return t
		}
		// bare name like ".zshrc"
		return filepath.Join(homeDir, t)
	}

	var result []Pair
	var unknown []string
	for _, t := range targets {
		norm := normalize(t)
		found := false
		for _, p := range pairs {
			if p.Dst == norm {
				result = append(result, p)
				found = true
				break
			}
		}
		if !found {
			unknown = append(unknown, t)
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("no tracked dotfile(s) matched: %s", strings.Join(unknown, ", "))
	}
	return result, nil
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
