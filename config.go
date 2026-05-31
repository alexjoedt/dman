package dman

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrNoConfig = errors.New("no dman config found, run: dman init <repo-url>")

// SnapshotConfig controls the snapshot feature.
// Snapshots are enabled by default.
type SnapshotConfig struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path,omitempty"` // default: ~/.local/share/dman/snapshots
}

type Config struct {
	RepositoryURL string          `json:"repositoryURL"`
	Profile       string          `json:"profile"`
	Path          string          `json:"path"`
	Snapshots     *SnapshotConfig `json:"snapshots,omitempty"`
}

const configFileName = "dman.json"

func (a *App) saveConfig(config *Config) error {
	name := filepath.Join(a.ConfigDir, configFileName)
	f, err := os.Create(name)
	if err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	defer f.Close()

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
	defer f.Close()

	var config Config
	if err := json.NewDecoder(f).Decode(&config); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if config.Snapshots == nil {
		config.Snapshots = &SnapshotConfig{Enabled: true}
	}
	return &config, nil
}
