package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/alexjoedt/dman/internal/profile"
)

// validKeys lists all user-settable config keys in display order.
var validKeys = []string{
	"profile",
	"addSymlinks",
	"git.autoAdd",
	"git.autoCommit",
	"git.autoPush",
	"snapshots.enabled",
	"snapshots.path",
}

type configAccessor struct {
	get   func(*Config) string
	set   func(*Config, string) error
	unset func(*Config)
}

func buildConfigAccessors() map[string]configAccessor {
	return map[string]configAccessor{
		"profile": {
			get:   func(c *Config) string { return c.Profile },
			set:   func(c *Config, v string) error { c.Profile = v; return nil },
			unset: func(c *Config) { c.Profile = "" },
		},
		"addSymlinks": {
			get: func(c *Config) string { return strconv.FormatBool(c.AddSymlinks) },
			set: func(c *Config, v string) error {
				b, err := parseBool(v)
				if err != nil {
					return err
				}
				c.AddSymlinks = b
				return nil
			},
			unset: func(c *Config) { c.AddSymlinks = false },
		},
		"git.autoAdd": {
			get: func(c *Config) string { return strconv.FormatBool(c.Git.AutoAdd) },
			set: func(c *Config, v string) error {
				b, err := parseBool(v)
				if err != nil {
					return err
				}
				c.Git.AutoAdd = b
				return nil
			},
			unset: func(c *Config) { c.Git.AutoAdd = false },
		},
		"git.autoCommit": {
			get: func(c *Config) string { return strconv.FormatBool(c.Git.AutoCommit) },
			set: func(c *Config, v string) error {
				b, err := parseBool(v)
				if err != nil {
					return err
				}
				c.Git.AutoCommit = b
				return nil
			},
			unset: func(c *Config) { c.Git.AutoCommit = false },
		},
		"git.autoPush": {
			get: func(c *Config) string { return strconv.FormatBool(c.Git.AutoPush) },
			set: func(c *Config, v string) error {
				b, err := parseBool(v)
				if err != nil {
					return err
				}
				c.Git.AutoPush = b
				return nil
			},
			unset: func(c *Config) { c.Git.AutoPush = false },
		},
		"snapshots.enabled": {
			get: func(c *Config) string { return strconv.FormatBool(c.Snapshots.Enabled) },
			set: func(c *Config, v string) error {
				b, err := parseBool(v)
				if err != nil {
					return err
				}
				c.Snapshots.Enabled = b
				return nil
			},
			unset: func(c *Config) { c.Snapshots.Enabled = false },
		},
		"snapshots.path": {
			get:   func(c *Config) string { return c.Snapshots.Path },
			set:   func(c *Config, v string) error { c.Snapshots.Path = v; return nil },
			unset: func(c *Config) { c.Snapshots.Path = "" },
		},
	}
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value %q: use true/false, 1/0, or yes/no", s)
	}
}

func errUnknownKey(key string) error {
	return fmt.Errorf("unknown key %q\nvalid keys: %s", key, strings.Join(validKeys, ", "))
}

// ConfigShow prints all settable config keys and their current values.
func (a *App) ConfigShow(ctx context.Context) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	accessors := buildConfigAccessors()
	maxLen := 0
	for _, k := range validKeys {
		if len(k) > maxLen {
			maxLen = len(k)
		}
	}
	for _, k := range validKeys {
		fmt.Printf("%-*s = %s\n", maxLen, k, accessors[k].get(cfg))
	}
	return nil
}

// ConfigGet prints the value of a single config key.
func (a *App) ConfigGet(ctx context.Context, key string) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	accessors := buildConfigAccessors()
	acc, ok := accessors[key]
	if !ok {
		return errUnknownKey(key)
	}
	fmt.Println(acc.get(cfg))
	return nil
}

// ConfigSet sets the value of a config key and persists the change.
func (a *App) ConfigSet(ctx context.Context, key, value string) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	accessors := buildConfigAccessors()
	acc, ok := accessors[key]
	if !ok {
		return errUnknownKey(key)
	}
	if err := acc.set(cfg, value); err != nil {
		return err
	}
	return a.saveConfig(cfg)
}

// ConfigUnset resets a config key to its zero value and persists the change.
func (a *App) ConfigUnset(ctx context.Context, key string) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	accessors := buildConfigAccessors()
	acc, ok := accessors[key]
	if !ok {
		return errUnknownKey(key)
	}
	acc.unset(cfg)
	return a.saveConfig(cfg)
}

// ConfigListProfiles lists the profile directories found in the dotfile repository.
// The active profile (from config) is prefixed with "* "; others with "  ".
// A profile with a parent is followed by its inheritance chain, nearest first,
// e.g. "arch-gridx -> arch". A broken chain is reported inline so one bad
// profile does not hide the others.
func (a *App) ConfigListProfiles(ctx context.Context) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	names, err := profile.List(cfg.Path)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Println("no profiles found")
		return nil
	}
	for _, name := range names {
		marker := "  "
		if name == cfg.Profile {
			marker = "* "
		}
		line := marker + name
		parents, err := profile.Parents(cfg.Path, name)
		switch {
		case err != nil:
			line += "  (error: " + err.Error() + ")"
		case len(parents) > 0:
			line += " -> " + strings.Join(parents, " -> ")
		}
		fmt.Println(line)
	}
	return nil
}

// ProfileInherit declares parent as the parent of child by writing
// profiles/<child>/profile.json. The child directory is created when missing;
// the parent must exist. A declaration that would close a cycle is rolled
// back. With clear set, the parent declaration of child is removed.
func (a *App) ProfileInherit(ctx context.Context, child, parent string, clear bool) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	if child == "" {
		return errors.New("profile name required")
	}
	if clear {
		if err := profile.WriteMeta(cfg.Path, child, profile.Meta{}); err != nil {
			return err
		}
		fmt.Printf("cleared parent of %s\n", child)
		return nil
	}
	if parent == "" {
		return errors.New("parent profile required")
	}
	if child == parent {
		return fmt.Errorf("profile %q cannot inherit itself", child)
	}
	if !profile.Exists(cfg.Path, parent) {
		return fmt.Errorf("parent profile %q does not exist", parent)
	}
	prev, err := profile.ReadMeta(cfg.Path, child)
	if err != nil {
		return err
	}
	if err := profile.WriteMeta(cfg.Path, child, profile.Meta{Inherits: parent}); err != nil {
		return err
	}
	if _, err := profile.Chain(cfg.Path, child); err != nil {
		if rbErr := profile.WriteMeta(cfg.Path, child, prev); rbErr != nil {
			return fmt.Errorf("%w (rollback failed: %v)", err, rbErr)
		}
		return err
	}
	fmt.Printf("%s -> %s\n", child, parent)
	return nil
}

// ProfileSet sets the active profile in config.
func (a *App) ProfileSet(ctx context.Context, name string) error {
	return a.ConfigSet(ctx, "profile", name)
}
