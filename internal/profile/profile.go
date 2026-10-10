// Package profile resolves the layout and inheritance of profiles inside a
// dotfile repository. A profile is a directory under profiles/<name>. It may
// carry a profile.json declaring a parent profile; the effective file set of a
// profile is the repository root, then every ancestor from the top down, then
// the profile itself, with later layers overriding earlier ones. A standalone
// profile drops the repository root from that stack, except for the paths its
// Meta.Root list keeps.
package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/alexjoedt/dman/internal/fsutil"
)

const (
	dirName  = "profiles"
	metaFile = "profile.json"
)

// Meta is the content of profiles/<name>/profile.json.
type Meta struct {
	Inherits string `json:"inherits,omitempty"`
	// Standalone excludes the repository root from the effective file set of
	// this profile and of every profile inheriting it.
	Standalone bool `json:"standalone,omitempty"`
	// Root lists repository-root paths a standalone profile keeps.
	Root []string `json:"root,omitempty"`
}

// IsZero reports whether the metadata carries no settings.
func (m Meta) IsZero() bool {
	if len(m.Root) == 0 {
		m.Root = nil
	}
	return reflect.DeepEqual(m, Meta{})
}

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
	if err := fsutil.WriteJSON(path, m); err != nil {
		return fmt.Errorf("write %s of profile %q: %w", metaFile, name, err)
	}
	return nil
}

// Chain resolves the inheritance chain of name, ordered from the root-most
// ancestor to name itself. A profile without a directory is returned as a
// single-element chain so callers can keep their base-only fallback. A declared
// parent whose directory is missing, or a cycle, is an error.
func Chain(repo, name string) ([]string, error) {
	chain, _, err := walk(repo, name)
	return chain, err
}

// walk is Chain that also returns the meta of every layer, in chain order.
func walk(repo, name string) ([]string, []Meta, error) {
	if name == "" {
		return nil, nil, nil
	}
	var chain []string
	var metas []Meta
	seen := map[string]bool{}
	for cur := name; cur != ""; {
		if seen[cur] {
			chain = append(chain, cur)
			return nil, nil, fmt.Errorf("profile inheritance cycle: %s", strings.Join(chain, " -> "))
		}
		seen[cur] = true
		chain = append(chain, cur)
		m, err := ReadMeta(repo, cur)
		if err != nil {
			return nil, nil, err
		}
		if m.Inherits != "" && !Exists(repo, m.Inherits) {
			return nil, nil, fmt.Errorf("profile %q inherits %q which does not exist", cur, m.Inherits)
		}
		metas = append(metas, m)
		cur = m.Inherits
	}
	return reverse(chain), reverse(metas), nil
}

// Info is the resolved view of a profile's inheritance chain.
type Info struct {
	// Chain runs from the root-most ancestor to the profile itself.
	Chain []string
	// Standalone is set when any profile in Chain is standalone.
	Standalone bool
	// Root is the union of every layer's Root entries, root-most ancestor
	// first, without duplicates. It is filled even when Standalone is false.
	Root []string
}

// Parents returns the ancestors of the profile, nearest first, or nil when it
// has no parent.
func (i Info) Parents() []string {
	if len(i.Chain) <= 1 {
		return nil
	}
	return reverse(i.Chain[:len(i.Chain)-1])
}

// Resolve reads every layer of name once and returns its chain, whether the
// repository root is excluded and the union of the Root include entries. An
// empty or bare "~" Root entry is an error: it would match every root file or
// none.
func Resolve(repo, name string) (Info, error) {
	chain, metas, err := walk(repo, name)
	if err != nil {
		return Info{}, err
	}
	info := Info{Chain: chain}
	seen := map[string]bool{}
	for i, m := range metas {
		info.Standalone = info.Standalone || m.Standalone
		for _, e := range m.Root {
			if strings.TrimSpace(e) == "" || e == "~" {
				return Info{}, fmt.Errorf("profile %q: invalid root entry %q", chain[i], e)
			}
			if !seen[e] {
				seen[e] = true
				info.Root = append(info.Root, e)
			}
		}
	}
	return info, nil
}

// RootIncludes reports whether the repository root is excluded from the
// effective file set of name and returns the union of the Root entries; see
// Resolve.
func RootIncludes(repo, name string) (standalone bool, root []string, err error) {
	info, err := Resolve(repo, name)
	return info.Standalone, info.Root, err
}

// Standalone reports whether the repository root is excluded from the
// effective file set of name, which is the case when any profile in its
// inheritance chain is marked standalone.
func Standalone(repo, name string) (bool, error) {
	info, err := Resolve(repo, name)
	return info.Standalone, err
}

// Parents returns the ancestors of name, nearest first, or nil when name has no
// parent. It is a convenience over Chain for display purposes.
func Parents(repo, name string) ([]string, error) {
	chain, err := Chain(repo, name)
	if err != nil {
		return nil, err
	}
	return Info{Chain: chain}.Parents(), nil
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

func reverse[T any](s []T) []T {
	out := make([]T, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}
