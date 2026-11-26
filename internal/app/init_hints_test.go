package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initWithFiles(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin")
	initGitRepo(t, origin)
	for _, f := range files {
		p := filepath.Join(origin, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"-C", origin, "add", "."},
		{"-C", origin, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	a := &App{HomeDir: dir, ConfigDir: filepath.Join(dir, "config"), HomeMode: 0o755}
	buf := captureLog(t)
	if err := a.Init(context.Background(), origin, filepath.Join(dir, "dots")); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return buf.String()
}

func TestInit_NextSteps(t *testing.T) {
	const profilesSet = "dman profiles set <name>"
	const identity = "dman config encryption.age.identity <path>"
	tests := []struct {
		name         string
		files        []string
		wantProfiles bool
		wantIdentity bool
	}{
		{"plain", []string{"dot_zshrc"}, false, false},
		{"profiles", []string{"dot_zshrc", "profiles/work/dot_vimrc"}, true, false},
		{"crypt", []string{"dot_zshrc", "dot_netrc.crypt"}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DMAN_AGE_IDENTITY", "")
			out := initWithFiles(t, tt.files...)
			if !strings.Contains(out, "dman apply") || !strings.Contains(out, "dman doctor") {
				t.Errorf("missing apply/doctor lines:\n%s", out)
			}
			if got := strings.Contains(out, profilesSet); got != tt.wantProfiles {
				t.Errorf("profiles hint = %t, want %t\n%s", got, tt.wantProfiles, out)
			}
			if tt.wantProfiles && !strings.Contains(out, "work") {
				t.Errorf("profile name missing:\n%s", out)
			}
			if got := strings.Contains(out, identity); got != tt.wantIdentity {
				t.Errorf("identity hint = %t, want %t\n%s", got, tt.wantIdentity, out)
			}
		})
	}
}

func TestInit_NextStepsSkipIdentityWhenEnvSet(t *testing.T) {
	t.Setenv("DMAN_AGE_IDENTITY", "/some/key")
	out := initWithFiles(t, "dot_netrc.crypt")
	if strings.Contains(out, "encryption.age.identity") {
		t.Errorf("identity hint printed with DMAN_AGE_IDENTITY set:\n%s", out)
	}
}

func TestInit_FailedInitPrintsNoHints(t *testing.T) {
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin")
	initGitRepo(t, origin)
	if err := os.WriteFile(filepath.Join(origin, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-C", origin, "add", "."},
		{"-C", origin, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	a := &App{HomeDir: dir, ConfigDir: filepath.Join(dir, "config"), HomeMode: 0o755}
	buf := captureLog(t)
	if err := a.Init(context.Background(), origin, filepath.Join(dir, "dots")); err == nil {
		t.Fatal("Init succeeded on a repo without dotfiles")
	}
	if strings.Contains(buf.String(), "dman apply") {
		t.Errorf("hints printed on failure:\n%s", buf.String())
	}
}
