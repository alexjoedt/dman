// Package profile resolves the layout and inheritance of profiles inside a
// dotfile repository. A profile is a directory under profiles/<name>. It may
// carry a profile.json declaring a parent profile; the effective file set of a
// profile is the repository root, then every ancestor from the top down, then
// the profile itself, with later layers overriding earlier ones.
package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	dirName  = "profiles"
	metaFile = "profile.json"
)

// Meta is the content of profiles/<name>/profile.json.
type Meta struct {
	Inherits string `json:"inherits,omitempty"`
}

// IsZero reports whether the metadata carries no settings.
func (m Meta) IsZero() bool { return m == Meta{} }

// Root returns the profiles/ directory of repo.
func Root(repo string) string { return filepath.Join(repo, dirName) }

// Dir returns the directory inside repo where files for the given profile are
// stored. An empty name maps to the repository root (base); any named profile
// maps under profiles/<name>.
func Dir(repo, name string) string {
	if name == "" {
		return repo
	}
	return filepath.Join(repo, dirName, name)
}

// Exists reports whether the profile directory is present in repo.
func Exists(repo, name string) bool {
	fi, err := os.Stat(Dir(repo, name))
	return err == nil && fi.IsDir()
}

// ReadMeta reads profiles/<name>/profile.json. A missing file yields a zero
// Meta and no error.
func ReadMeta(repo, name string) (Meta, error) {
	var m Meta
	data, err := os.ReadFile(filepath.Join(Dir(repo, name), metaFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return m, nil
		}
		return m, fmt.Errorf("read %s of profile %q: %w", metaFile, name, err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("parse %s of profile %q: %w", metaFile, name, err)
	}
	return m, nil
}

// WriteMeta writes profiles/<name>/profile.json, creating the profile directory
// if needed. A zero Meta removes the file instead.
func WriteMeta(repo, name string, m Meta) error {
	if name == "" {
		return errors.New("profile name required")
	}
	dir := Dir(repo, name)
	path := filepath.Join(dir, metaFile)
	if m.IsZero() {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s of profile %q: %w", metaFile, name, err)
		}
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create profile %q: %w", name, err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s of profile %q: %w", metaFile, name, err)
	}
	return nil
}

// Chain resolves the inheritance chain of name, ordered from the root-most
// ancestor to name itself. A profile without a directory is returned as a
// single-element chain so callers can keep their base-only fallback. A declared
// parent whose directory is missing, or a cycle, is an error.
func Chain(repo, name string) ([]string, error) {
	if name == "" {
		return nil, nil
	}
	var chain []string
	seen := map[string]bool{}
	for cur := name; cur != ""; {
		if seen[cur] {
			chain = append(chain, cur)
			return nil, fmt.Errorf("profile inheritance cycle: %s", strings.Join(chain, " -> "))
		}
		seen[cur] = true
		chain = append(chain, cur)
		m, err := ReadMeta(repo, cur)
		if err != nil {
			return nil, err
		}
		if m.Inherits != "" && !Exists(repo, m.Inherits) {
			return nil, fmt.Errorf("profile %q inherits %q which does not exist", cur, m.Inherits)
		}
		cur = m.Inherits
	}
	return reverse(chain), nil
}

// Parents returns the ancestors of name, nearest first, or nil when name has no
// parent. It is a convenience over Chain for display purposes.
func Parents(repo, name string) ([]string, error) {
	chain, err := Chain(repo, name)
	if err != nil {
		return nil, err
	}
	if len(chain) <= 1 {
		return nil, nil
	}
	return reverse(chain[:len(chain)-1]), nil
}

// List returns the names of all profile directories in repo, sorted. A missing
// profiles/ directory yields nil and no error.
func List(repo string) ([]string, error) {
	entries, err := os.ReadDir(Root(repo))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func reverse(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}
