package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newConfigTestApp creates a minimal App with a temp config dir and a saved config.
// It also creates a minimal repo directory structure for profile tests.
func newConfigTestApp(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("setup repo dir: %v", err)
	}
	a := &App{ConfigDir: dir}
	cfg := &Config{
		RepositoryURL: "https://example.com/dotfiles.git",
		Profile:       "default",
		Path:          repoDir,
		Snapshots:     &SnapshotConfig{Enabled: true},
		Git:           &GitAutomationConfig{},
	}
	if err := a.saveConfig(cfg); err != nil {
		t.Fatalf("setup saveConfig: %v", err)
	}
	return a, repoDir
}

func TestConfigSet_Profile(t *testing.T) {
	a, _ := newConfigTestApp(t)
	ctx := context.Background()

	if err := a.ConfigSet(ctx, "profile", "arch"); err != nil {
		t.Fatalf("ConfigSet: %v", err)
	}

	cfg, err := a.readConfig()
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if cfg.Profile != "arch" {
		t.Errorf("Profile: want %q got %q", "arch", cfg.Profile)
	}
}

func TestConfigSet_BoolKey(t *testing.T) {
	a, _ := newConfigTestApp(t)
	ctx := context.Background()

	for _, val := range []string{"true", "1", "yes"} {
		if err := a.ConfigSet(ctx, "git.autoAdd", val); err != nil {
			t.Fatalf("ConfigSet git.autoAdd %q: %v", val, err)
		}
		cfg, err := a.readConfig()
		if err != nil {
			t.Fatalf("readConfig: %v", err)
		}
		if !cfg.Git.AutoAdd {
			t.Errorf("git.autoAdd: want true after setting %q", val)
		}
	}

	for _, val := range []string{"false", "0", "no"} {
		if err := a.ConfigSet(ctx, "git.autoAdd", val); err != nil {
			t.Fatalf("ConfigSet git.autoAdd %q: %v", val, err)
		}
		cfg, err := a.readConfig()
		if err != nil {
			t.Fatalf("readConfig: %v", err)
		}
		if cfg.Git.AutoAdd {
			t.Errorf("git.autoAdd: want false after setting %q", val)
		}
	}
}

func TestConfigSet_InvalidBool(t *testing.T) {
	a, _ := newConfigTestApp(t)
	ctx := context.Background()

	err := a.ConfigSet(ctx, "git.autoAdd", "maybe")
	if err == nil {
		t.Fatal("expected error for invalid boolean, got nil")
	}
}

func TestConfigGet_UnknownKey(t *testing.T) {
	a, _ := newConfigTestApp(t)
	ctx := context.Background()

	err := a.ConfigGet(ctx, "notakey")
	if err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
}

func TestConfigSet_UnknownKey(t *testing.T) {
	a, _ := newConfigTestApp(t)
	ctx := context.Background()

	err := a.ConfigSet(ctx, "notakey", "value")
	if err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
}

func TestConfigUnset_Profile(t *testing.T) {
	a, _ := newConfigTestApp(t)
	ctx := context.Background()

	// Set a non-empty value first.
	if err := a.ConfigSet(ctx, "profile", "laptop"); err != nil {
		t.Fatalf("ConfigSet: %v", err)
	}

	if err := a.ConfigUnset(ctx, "profile"); err != nil {
		t.Fatalf("ConfigUnset: %v", err)
	}

	cfg, err := a.readConfig()
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if cfg.Profile != "" {
		t.Errorf("Profile after unset: want %q got %q", "", cfg.Profile)
	}
}

func TestConfigUnset_BoolKey(t *testing.T) {
	a, _ := newConfigTestApp(t)
	ctx := context.Background()

	if err := a.ConfigSet(ctx, "snapshots.enabled", "false"); err != nil {
		t.Fatalf("ConfigSet: %v", err)
	}
	if err := a.ConfigUnset(ctx, "snapshots.enabled"); err != nil {
		t.Fatalf("ConfigUnset: %v", err)
	}

	cfg, err := a.readConfig()
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if cfg.Snapshots.Enabled {
		t.Error("snapshots.enabled after unset: want false got true")
	}
}

func TestConfigListProfiles(t *testing.T) {
	a, repoDir := newConfigTestApp(t)
	ctx := context.Background()

	// Active profile is "default" (set in newConfigTestApp).
	for _, name := range []string{"arch", "default", "work"} {
		if err := os.MkdirAll(filepath.Join(repoDir, "profiles", name), 0o755); err != nil {
			t.Fatalf("setup profile dir %q: %v", name, err)
		}
	}
	// A regular file — should not appear in output.
	if err := os.WriteFile(filepath.Join(repoDir, "profiles", "README"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup file: %v", err)
	}

	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := a.ConfigListProfiles(ctx)
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("ConfigListProfiles: %v", err)
	}

	output := buf.String()
	for _, name := range []string{"arch", "default", "work"} {
		if !strings.Contains(output, name) {
			t.Errorf("output missing profile %q", name)
		}
	}
	// "default" should be marked as active.
	if !strings.Contains(output, "* default") {
		t.Errorf("active profile 'default' not marked with '*' in output:\n%s", output)
	}
	// "arch" and "work" should not be marked.
	for _, name := range []string{"arch", "work"} {
		if strings.Contains(output, "* "+name) {
			t.Errorf("inactive profile %q should not be marked with '*'", name)
		}
	}
	// File "README" should not appear.
	if strings.Contains(output, "README") {
		t.Errorf("non-directory 'README' should not appear in output")
	}
}

func TestConfigListProfiles_NoProfilesDir(t *testing.T) {
	a, _ := newConfigTestApp(t)
	ctx := context.Background()

	// profiles/ directory does not exist — should print "no profiles found", not error.
	if err := a.ConfigListProfiles(ctx); err != nil {
		t.Fatalf("ConfigListProfiles with missing dir: %v", err)
	}
}

func TestParseBool(t *testing.T) {
	tests := []struct {
		input   string
		want    bool
		wantErr bool
	}{
		{"true", true, false},
		{"True", true, false},
		{"TRUE", true, false},
		{"1", true, false},
		{"yes", true, false},
		{"false", false, false},
		{"False", false, false},
		{"0", false, false},
		{"no", false, false},
		{"maybe", false, true},
		{"", false, true},
		{"on", false, true},
	}
	for _, tt := range tests {
		got, err := parseBool(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseBool(%q) error = %v, wantErr %t", tt.input, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("parseBool(%q) = %t, want %t", tt.input, got, tt.want)
		}
	}
}

func TestConfigShow_NoError(t *testing.T) {
	a, _ := newConfigTestApp(t)
	if err := a.ConfigShow(context.Background()); err != nil {
		t.Fatalf("ConfigShow: %v", err)
	}
}

func TestConfigSet_AddSymlinks(t *testing.T) {
	a, _ := newConfigTestApp(t)
	ctx := context.Background()

	if err := a.ConfigSet(ctx, "addSymlinks", "true"); err != nil {
		t.Fatalf("ConfigSet addSymlinks true: %v", err)
	}
	cfg, err := a.readConfig()
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if !cfg.AddSymlinks {
		t.Error("AddSymlinks: want true got false")
	}

	if err := a.ConfigSet(ctx, "addSymlinks", "false"); err != nil {
		t.Fatalf("ConfigSet addSymlinks false: %v", err)
	}
	cfg, err = a.readConfig()
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if cfg.AddSymlinks {
		t.Error("AddSymlinks: want false got true")
	}
}

func TestConfigUnset_AddSymlinks(t *testing.T) {
	a, _ := newConfigTestApp(t)
	ctx := context.Background()

	if err := a.ConfigSet(ctx, "addSymlinks", "true"); err != nil {
		t.Fatalf("ConfigSet: %v", err)
	}
	if err := a.ConfigUnset(ctx, "addSymlinks"); err != nil {
		t.Fatalf("ConfigUnset: %v", err)
	}
	cfg, err := a.readConfig()
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if cfg.AddSymlinks {
		t.Error("AddSymlinks after unset: want false got true")
	}
}

func TestProfileSet(t *testing.T) {
	a, _ := newConfigTestApp(t)
	ctx := context.Background()

	if err := a.ProfileSet(ctx, "work"); err != nil {
		t.Fatalf("ProfileSet: %v", err)
	}

	cfg, err := a.readConfig()
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if cfg.Profile != "work" {
		t.Errorf("Profile: want %q got %q", "work", cfg.Profile)
	}
}
