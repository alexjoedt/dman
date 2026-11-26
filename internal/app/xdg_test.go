package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
}

func touch(t *testing.T, p string) {
	t.Helper()
	mkdirs(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func TestXDGPath(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	legacyCfg := filepath.Join(home, ".config", "dman")
	legacyState := filepath.Join(home, ".local", "state", "dman")
	legacyData := filepath.Join(home, ".local", "share", "dman")

	type kind struct {
		env, legacy, marker string
		legacyDir           string
		create              func(t *testing.T, dir string)
	}
	config := kind{"XDG_CONFIG_HOME", ".config", configFileName, legacyCfg,
		func(t *testing.T, dir string) { touch(t, filepath.Join(dir, configFileName)) }}
	state := kind{"XDG_STATE_HOME", filepath.Join(".local", "state"), "snapshots", legacyState,
		func(t *testing.T, dir string) { mkdirs(t, filepath.Join(dir, "snapshots")) }}
	brokenCfg := kind{"XDG_CONFIG_HOME", ".config", configFileName, legacyCfg,
		func(t *testing.T, dir string) {
			mkdirs(t, dir)
			if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, configFileName)); err != nil {
				t.Fatalf("symlink: %v", err)
			}
		}}
	data := kind{"XDG_DATA_HOME", filepath.Join(".local", "share"), "", legacyData,
		func(t *testing.T, dir string) { mkdirs(t, dir) }}

	tests := []struct {
		name   string
		kind   kind
		value  string
		legacy bool // marker exists under the legacy dir
		inXDG  bool // marker exists under the XDG dir
		want   string
	}{
		{"config set", config, xdg, false, false, filepath.Join(xdg, "dman")},
		{"config unset", config, "", false, false, legacyCfg},
		{"config relative", config, "rel/cfg", false, false, legacyCfg},
		{"config legacy only", config, xdg, true, false, legacyCfg},
		{"config both", config, xdg, true, true, filepath.Join(xdg, "dman")},
		{"config broken symlink in xdg", brokenCfg, xdg, true, true, filepath.Join(xdg, "dman")},
		{"state set", state, xdg, false, false, filepath.Join(xdg, "dman")},
		{"state unset", state, "", false, false, legacyState},
		{"state relative", state, "rel", false, false, legacyState},
		{"state legacy only", state, xdg, true, false, legacyState},
		{"state both", state, xdg, true, true, filepath.Join(xdg, "dman")},
		{"data set", data, xdg, false, false, filepath.Join(xdg, "dman")},
		{"data unset", data, "", false, false, legacyData},
		{"data relative", data, "rel", false, false, legacyData},
		{"data legacy exists", data, xdg, true, false, filepath.Join(xdg, "dman")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, d := range []string{filepath.Join(home, ".config"), filepath.Join(home, ".local"), filepath.Join(xdg, "dman")} {
				if err := os.RemoveAll(d); err != nil {
					t.Fatalf("reset: %v", err)
				}
			}
			if tt.legacy {
				create := tt.kind.create
				if tt.kind.env == config.env {
					create = config.create
				}
				create(t, tt.kind.legacyDir)
			}
			if tt.inXDG {
				tt.kind.create(t, filepath.Join(xdg, "dman"))
			}
			t.Setenv(tt.kind.env, tt.value)
			if got := xdgPath(home, tt.kind.env, tt.kind.legacy, tt.kind.marker); got != tt.want {
				t.Errorf("xdgPath = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSnapshotStore_XDGStateHome(t *testing.T) {
	home := t.TempDir()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	a := &App{HomeDir: home, HomeMode: 0o755}
	if _, err := a.snapshotStore(&Config{Snapshots: &SnapshotConfig{Enabled: true}}); err != nil {
		t.Fatalf("snapshotStore: %v", err)
	}
	if !isExist(filepath.Join(state, "dman", "snapshots")) {
		t.Errorf("store not under XDG_STATE_HOME")
	}
	if isExist(filepath.Join(home, ".local")) {
		t.Errorf("legacy state dir created")
	}
}

func newXDGApp(t *testing.T) (a *App, home, cfgHome, dataHome string) {
	t.Helper()
	home = t.TempDir()
	cfgHome = t.TempDir()
	dataHome = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("XDG_DATA_HOME", dataHome)
	a, err := NewApp()
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	return a, home, cfgHome, dataHome
}

func TestInit_UsesXDGConfigAndDataHome(t *testing.T) {
	a, home, cfgHome, dataHome := newXDGApp(t)
	origin := filepath.Join(t.TempDir(), "origin")
	initGitRepo(t, origin)
	touch(t, filepath.Join(origin, "dot_zshrc"))
	for _, args := range [][]string{
		{"-C", origin, "add", "."},
		{"-C", origin, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	if err := a.Init(context.Background(), origin, ""); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !isExist(filepath.Join(cfgHome, "dman", configFileName)) {
		t.Fatalf("dman.json not under XDG_CONFIG_HOME")
	}
	if isExist(filepath.Join(home, ".config")) || isExist(filepath.Join(home, ".local")) {
		t.Errorf("legacy dirs created")
	}

	b, err := NewApp()
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	cfg, err := b.readConfig()
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if want := filepath.Join(dataHome, "dman"); cfg.Path != want {
		t.Errorf("Path = %q, want %q", cfg.Path, want)
	}
}

func TestNewApp_LegacyConfigLoadedAndSavedInPlace(t *testing.T) {
	home := t.TempDir()
	cfgHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	legacy := filepath.Join(home, ".config", "dman")
	touch(t, filepath.Join(legacy, configFileName))

	a, err := NewApp()
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	if a.ConfigDir != legacy {
		t.Fatalf("ConfigDir = %q, want %q", a.ConfigDir, legacy)
	}
	if err := a.saveConfig(&Config{Profile: "work"}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	cfg, err := a.readConfig()
	if err != nil || cfg.Profile != "work" {
		t.Fatalf("readConfig = %+v, %v", cfg, err)
	}
	if isExist(filepath.Join(cfgHome, "dman")) {
		t.Errorf("config moved or copied to XDG_CONFIG_HOME")
	}
}

func TestNewApp_NoConfigLeavesNoDir(t *testing.T) {
	a, home, cfgHome, _ := newXDGApp(t)
	if _, err := a.readConfig(); !errors.Is(err, ErrNoConfig) {
		t.Fatalf("readConfig: want ErrNoConfig, got %v", err)
	}
	if isExist(filepath.Join(cfgHome, "dman")) || isExist(filepath.Join(home, ".config")) {
		t.Errorf("config dir created without a save")
	}
}

func TestSaveConfig_CreatesConfigDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "xdg", "dman")
	a := &App{HomeMode: 0o700, ConfigDir: dir}
	if err := a.saveConfig(&Config{Profile: "default"}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	if !isExist(filepath.Join(dir, configFileName)) {
		t.Errorf("dman.json not written")
	}
}

func TestRemoveConfig(t *testing.T) {
	tests := []struct {
		name    string
		foreign bool
		tmp     bool
		legacy  bool
	}{
		{"empty dir removed", false, false, false},
		{"foreign file keeps dir", true, false, false},
		{"temp file leftover removed", false, true, false},
		{"shadowed legacy config removed", false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Config dir outside HomeDir, as with an XDG_CONFIG_HOME elsewhere.
			dir := filepath.Join(t.TempDir(), "dman")
			home := t.TempDir()
			a := &App{HomeDir: home, HomeMode: 0o755, ConfigDir: dir}
			if err := a.saveConfig(&Config{Profile: "default"}); err != nil {
				t.Fatalf("saveConfig: %v", err)
			}
			foreign := filepath.Join(dir, "notes.txt")
			if tt.foreign {
				touch(t, foreign)
			}
			if tt.tmp {
				touch(t, filepath.Join(dir, ".dman-123"))
			}
			legacy := filepath.Join(home, ".config", "dman")
			if tt.legacy {
				touch(t, filepath.Join(legacy, configFileName))
			}
			if err := a.removeConfig(); err != nil {
				t.Fatalf("removeConfig: %v", err)
			}
			if isExist(filepath.Join(dir, configFileName)) {
				t.Errorf("dman.json survived")
			}
			if isExist(dir) != tt.foreign {
				t.Errorf("dir exists = %v, want %v", isExist(dir), tt.foreign)
			}
			if tt.foreign && !isExist(foreign) {
				t.Errorf("foreign file removed")
			}
			if isExist(legacy) {
				t.Errorf("legacy config dir survived")
			}
		})
	}
}

func TestReadConfig_BrokenSymlinkIsNotMissing(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, configFileName)); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	a := &App{HomeDir: t.TempDir(), ConfigDir: dir}
	if _, err := a.readConfig(); err == nil || errors.Is(err, ErrNoConfig) {
		t.Errorf("readConfig = %v, want a read error", err)
	}
}
