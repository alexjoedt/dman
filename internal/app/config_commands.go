package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// validKeys lists all user-settable config keys in display order.
var validKeys = []string{
	"profile",
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
func (a *App) ConfigListProfiles(ctx context.Context) error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	profilesDir := filepath.Join(cfg.Path, "profiles")
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println("no profiles found")
			return nil
		}
		return fmt.Errorf("list profiles: %w", err)
	}
	found := false
	for _, e := range entries {
		if e.IsDir() {
			found = true
			marker := "  "
			if e.Name() == cfg.Profile {
				marker = "* "
			}
			fmt.Printf("%s%s\n", marker, e.Name())
		}
	}
	if !found {
		fmt.Println("no profiles found")
	}
	return nil
}

// ProfileSet sets the active profile in config.
func (a *App) ProfileSet(ctx context.Context, name string) error {
	return a.ConfigSet(ctx, "profile", name)
}
