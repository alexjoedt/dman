package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexjoedt/dman/internal/fsutil"
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

// EncryptionConfig holds the key used for files added with --encrypt.
// Encryption is enabled as soon as an identity is configured; without one
// encrypted files are skipped on apply.
type EncryptionConfig struct {
	Age AgeConfig `json:"age"`
}

// AgeConfig points at the age identity file. Its recipient is derived from
// the identity, so no public key has to be configured separately.
type AgeConfig struct {
	Identity string `json:"identity,omitempty"` // path, "~/" is expanded
}

// Config holds the persisted dman configuration.
type Config struct {
	RepositoryURL string               `json:"repositoryURL"`
	Profile       string               `json:"profile"`
	Path          string               `json:"path"`
	Snapshots     *SnapshotConfig      `json:"snapshots,omitempty"`
	Git           *GitAutomationConfig `json:"git,omitempty"`
	Encryption    *EncryptionConfig    `json:"encryption,omitempty"`
}

const configFileName = "dman.json"

func (a *App) saveConfig(config *Config) error {
	name := filepath.Join(a.ConfigDir, configFileName)
	if err := fsutil.WriteJSON(name, config); err != nil {
		return fmt.Errorf("save config: %w", err)
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
	if config.Path != "" {
		config.Path = filepath.Clean(config.Path)
	}
	if config.Snapshots == nil {
		config.Snapshots = &SnapshotConfig{Enabled: true}
	}
	if config.Git == nil {
		config.Git = &GitAutomationConfig{}
	}
	if config.Encryption == nil {
		config.Encryption = &EncryptionConfig{}
	}
	// The push => commit => add cascade is applied in resolveAddGitOps at use
	// time. Applying it here would persist derived values on the next save
	// and make unsetting a parent flag ineffective.
	return &config, nil
}
