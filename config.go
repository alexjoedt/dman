package dman

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrNoConfig = errors.New("no dman config found, initialized?")

type Config struct {
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	Path       string `json:"path"`
}

func (a *App) saveConfig(config *Config) error {
	name := filepath.Join(a.ConfigDir, "config")
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
	name := filepath.Join(a.ConfigDir, "config")
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
	return &config, nil
}

