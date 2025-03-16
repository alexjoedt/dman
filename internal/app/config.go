package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNoConfig is returned when the dman config file does not exist.
var ErrNoConfig = errors.New("no dman config found, run: dman init <repo-url>")

// SnapshotConfig controls the snapshot feature.
// Snapshots are enabled by default.
type SnapshotConfig struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path,omitempty"` // default: ~/.local/state/dman/snapshots
}

// GitAutomationConfig controls which git steps run during dman add flows.
type GitAutomationConfig struct {
	AutoAdd    bool `json:"autoAdd"`
	AutoCommit bool `json:"autoCommit"`
	AutoPush   bool `json:"autoPush"`
}

// Config holds the persisted dman configuration.
type Config struct {
	RepositoryURL string               `json:"repositoryURL"`
	Profile       string               `json:"profile"`
	Path          string               `json:"path"`
	Snapshots     *SnapshotConfig      `json:"snapshots,omitempty"`
	Git           *GitAutomationConfig `json:"git,omitempty"`
}

const configFileName = "dman.json"

func (a *App) saveConfig(config *Config) error {
	name := filepath.Join(a.ConfigDir, configFileName)
	f, err := os.Create(name)
	if err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	defer func() { _ = f.Close() }()

	d := json.NewEncoder(f)
	d.SetIndent("", "  ")
	if err := d.Encode(config); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return nil
}

func (a *App) readConfig() (*Config, error) {
	name := filepath.Join(a.ConfigDir, configFileName)
	if !isExist(name) {
		return nil, ErrNoConfig
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	var config Config
	if err := json.NewDecoder(f).Decode(&config); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if config.Snapshots == nil {
		config.Snapshots = &SnapshotConfig{Enabled: true}
	}
	if config.Git == nil {
		config.Git = &GitAutomationConfig{}
	}
	config.Git.normalize()
	return &config, nil
}

func (g *GitAutomationConfig) normalize() {
	if g == nil {
		return
	}
	if g.AutoPush {
		g.AutoCommit = true
	}
	if g.AutoCommit {
		g.AutoAdd = true
	}
}
