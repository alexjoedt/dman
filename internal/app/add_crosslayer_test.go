package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexjoedt/dman/internal/crypt"
	"github.com/alexjoedt/dman/internal/profile"
)

// A .crypt in any layer of the repository makes add encrypt the file in the
// target layer, even without --encrypt.
func TestAdd_CryptInOtherLayerEncrypts(t *testing.T) {
	tests := []struct {
		name       string
		tracked    string // layer file holding the .crypt
		profile    string // profile flag
		root       bool
		active     string // active profile
		standalone bool   // active profile is standalone
		inherits   string // parent of profile work
		want       string
	}{
		{name: "base, profile flag", tracked: "dot_netrc.crypt", profile: "work", want: "profiles/work/dot_netrc"},
		{name: "base, active standalone", tracked: "dot_netrc.crypt", active: "work", standalone: true, want: "profiles/work/dot_netrc"},
		{name: "descendant, root flag", tracked: "profiles/work/dot_netrc.crypt", root: true, want: "dot_netrc"},
		{name: "sibling profile", tracked: "profiles/home/dot_netrc.crypt", profile: "work", want: "profiles/work/dot_netrc"},
		{name: "ancestor profile", tracked: "profiles/common/dot_netrc.crypt", profile: "work", inherits: "common", want: "profiles/work/dot_netrc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupCryptFixture(t, true)
			if tt.active != "" {
				e.cfg.Profile = tt.active
				if err := e.app.saveConfig(e.cfg); err != nil {
					t.Fatal(err)
				}
			}
			if tt.standalone || tt.inherits != "" {
				if err := profile.WriteMeta(e.repo, "work", profile.Meta{Standalone: tt.standalone, Inherits: tt.inherits}); err != nil {
					t.Fatal(err)
				}
			}
			e.encryptRepo(t, tt.tracked, "old\n")
			src := e.writeHome(t, ".netrc", "secret\n")

			buf := captureLog(t)
			if err := e.app.Add(context.Background(), []string{src}, tt.profile, tt.root, false, false, false, false); err != nil {
				t.Fatalf("Add: %v", err)
			}
			if isExist(filepath.Join(e.repo, tt.want)) {
				t.Errorf("plaintext %s was written", tt.want)
			}
			if got := e.decryptRepo(t, tt.want+".crypt"); got != "secret\n" {
				t.Errorf("decrypted = %q", got)
			}
			if got := strings.Count(buf.String(), "as="+tt.tracked+"\n"); got != 1 {
				t.Errorf("info lines naming %s = %d, log = %q", tt.tracked, got, buf.String())
			}
		})
	}
}

func TestAdd_CryptInOtherLayerConvertsPlainCopy(t *testing.T) {
	e := setupCryptFixture(t, true)
	initGitRepo(t, e.repo)
	e.encryptRepo(t, "dot_netrc.crypt", "base\n")
	plain := e.writeRepo(t, "profiles/work/dot_netrc", "leaked\n")
	for _, args := range [][]string{
		{"-C", e.repo, "add", "."},
		{"-C", e.repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	src := e.writeHome(t, ".netrc", "secret\n")

	if err := e.app.Add(context.Background(), []string{src}, "work", false, false, true, false, false); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if isExist(plain) {
		t.Error("plain copy still exists")
	}
	if got := e.decryptRepo(t, "profiles/work/dot_netrc.crypt"); got != "secret\n" {
		t.Errorf("decrypted = %q", got)
	}
	out, err := exec.Command("git", "-C", e.repo, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	status := string(out)
	if !strings.Contains(status, "D  profiles/work/dot_netrc\n") || !strings.Contains(status, "A  profiles/work/dot_netrc.crypt\n") {
		t.Errorf("git status = %q, want staged delete of the plain copy and add of the .crypt", status)
	}
}

func TestAdd_CryptInOtherLayerWithoutKeyErrors(t *testing.T) {
	e := setupCryptFixture(t, false)
	e.encryptRepo(t, "dot_netrc.crypt", "base\n")
	src := e.writeHome(t, ".netrc", "secret\n")

	err := e.app.Add(context.Background(), []string{src}, "work", false, false, false, false, false)
	if !errors.Is(err, crypt.ErrNoKey) {
		t.Fatalf("Add err = %v, want ErrNoKey", err)
	}
	if isExist(filepath.Join(e.repo, "profiles/work/dot_netrc")) || isExist(filepath.Join(e.repo, "profiles/work/dot_netrc.crypt")) {
		t.Error("something was written without a key")
	}
}

func TestAdd_NoCryptAnywhereStaysPlain(t *testing.T) {
	e := setupCryptFixture(t, true)
	e.encryptRepo(t, "dot_other.crypt", "x\n")
	src := e.writeHome(t, ".netrc", "plain\n")

	buf := captureLog(t)
	if err := e.app.Add(context.Background(), []string{src}, "work", false, false, false, false, false); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := readFileString(t, filepath.Join(e.repo, "profiles/work/dot_netrc")); got != "plain\n" {
		t.Errorf("plain copy = %q", got)
	}
	if strings.Contains(buf.String(), "stores it encrypted") {
		t.Errorf("unexpected encryption info: %q", buf.String())
	}
}

// A missing key fails the add before any file of a directory is written.
func TestAdd_CryptInOtherLayerWithoutKeyWritesNothing(t *testing.T) {
	e := setupCryptFixture(t, false)
	e.encryptRepo(t, "profiles/work/dot_cfg/b.crypt", "work\n")
	e.writeHome(t, ".cfg/a", "plain\n")
	e.writeHome(t, ".cfg/b", "secret\n")

	err := e.app.Add(context.Background(), []string{filepath.Join(e.home, ".cfg")}, "", true, false, false, false, false)
	if !errors.Is(err, crypt.ErrNoKey) {
		t.Fatalf("Add err = %v, want ErrNoKey", err)
	}
	if isExist(filepath.Join(e.repo, "dot_cfg/a")) {
		t.Error("dot_cfg/a was written before the add failed")
	}
}

// Only a regular file named <rel>.crypt counts as an encrypted copy.
func TestAdd_CryptDirectoryInOtherLayerStaysPlain(t *testing.T) {
	e := setupCryptFixture(t, true)
	e.writeRepo(t, "profiles/home/dot_netrc.crypt/keep", "x\n")
	src := e.writeHome(t, ".netrc", "plain\n")

	if err := e.app.Add(context.Background(), []string{src}, "work", false, false, false, false, false); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := readFileString(t, filepath.Join(e.repo, "profiles/work/dot_netrc")); got != "plain\n" {
		t.Errorf("plain copy = %q", got)
	}
}

// A dangling .crypt symlink in the target layer is skipped without claiming
// that the file is being encrypted.
func TestAdd_DanglingCryptSymlinkLogsNoEncryption(t *testing.T) {
	e := setupCryptFixture(t, true)
	e.encryptRepo(t, "dot_netrc.crypt", "base\n")
	link := filepath.Join(e.repo, "profiles/work/dot_netrc.crypt")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(e.repo, "missing"), link); err != nil {
		t.Fatal(err)
	}
	src := e.writeHome(t, ".netrc", "secret\n")

	buf := captureLog(t)
	if err := e.app.Add(context.Background(), []string{src}, "work", false, false, false, false, false); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if strings.Contains(buf.String(), "stores it encrypted") {
		t.Errorf("encryption claimed for a skipped file: %q", buf.String())
	}
}
