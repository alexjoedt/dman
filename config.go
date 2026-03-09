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

func (a *App) openConfigFile() (*os.File, error) {
	name := filepath.Join(a.ConfigDir, "config")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, a.HomeMode)
	if err != nil {
		return nil, fmt.Errorf("create or open config file: %w", err)
	}
	return f, nil
}

func (a *App) saveConfig(config *Config) error {
	f, err := a.openConfigFile()
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
	if !isExist(filepath.Join(a.ConfigDir, "config")) {
		return nil, ErrNoConfig
	}
	f, err := a.openConfigFile()
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

