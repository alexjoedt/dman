package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/armor"
	"github.com/alexjoedt/dman/internal/crypt"
)

// doctorFixture is a healthy setup: git repo with a plain and an encrypted
// file, the key configured, snapshots enabled at a store that does not exist.
func doctorFixture(t *testing.T) *cryptEnv {
	t.Helper()
	e := setupCryptFixture(t, true)
	t.Setenv(crypt.EnvAgePassphrase, "")
	if err := os.Unsetenv(crypt.EnvAgePassphrase); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, e.repo)
	e.writeRepo(t, "dot_zshrc", "plain")
	e.encryptRepo(t, "dot_secret.crypt", "secret")
	e.cfg.Snapshots = &SnapshotConfig{Enabled: true, Path: filepath.Join(filepath.Dir(e.repo), "state", "snapshots")}
	e.save(t)
	return e
}

func (e *cryptEnv) save(t *testing.T) {
	t.Helper()
	if err := e.app.saveConfig(e.cfg); err != nil {
		t.Fatal(err)
	}
}

// protect replaces the configured identity with a passphrase-protected copy.
func (e *cryptEnv) protect(t *testing.T, pass string) {
	t.Helper()
	r, err := age.NewScryptRecipient(pass)
	if err != nil {
		t.Fatal(err)
	}
	r.SetWorkFactor(10)
	var buf bytes.Buffer
	aw := armor.NewWriter(&buf)
	w, err := age.Encrypt(aw, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(readFileString(t, e.identity))); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := aw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.identity, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runDoctor(t *testing.T, a *App) (string, error) {
	t.Helper()
	var err error
	out := captureStdout(t, func() { err = a.Doctor(context.Background(), "test") })
	return out, err
}

// doctorLine returns the status of the report line for check, or "".
func doctorLine(out, check string) string {
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 1 && f[1] == check {
			return f[0]
		}
	}
	return ""
}

func TestDoctor(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, e *cryptEnv)
		check   string
		status  string
		wantErr bool
	}{
		{name: "healthy", check: "key", status: "ok"},
		{
			name: "missing identity file",
			setup: func(t *testing.T, e *cryptEnv) {
				e.cfg.Encryption.Age.Identity = filepath.Join(t.TempDir(), "missing.txt")
				e.save(t)
			},
			check: "identity", status: "fail", wantErr: true,
		},
		{
			name: "no identity",
			setup: func(t *testing.T, e *cryptEnv) {
				e.cfg.Encryption.Age.Identity = ""
				e.save(t)
			},
			check: "identity", status: "warn",
		},
		{
			name: "wrong key",
			setup: func(t *testing.T, e *cryptEnv) {
				id, err := age.GenerateX25519Identity()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(e.identity, []byte(id.String()+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			check: "key", status: "fail", wantErr: true,
		},
		{
			name:  "protected identity without passphrase",
			setup: func(t *testing.T, e *cryptEnv) { e.protect(t, "pw") },
			check: "key", status: "warn",
		},
		{
			name: "protected identity with passphrase",
			setup: func(t *testing.T, e *cryptEnv) {
				e.protect(t, "pw")
				t.Setenv(crypt.EnvAgePassphrase, "pw")
			},
			check: "key", status: "ok",
		},
		{
			name: "snapshots disabled",
			setup: func(t *testing.T, e *cryptEnv) {
				e.cfg.Snapshots.Enabled = false
				e.save(t)
			},
			check: "snapshots", status: "warn",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := doctorFixture(t)
			if tt.setup != nil {
				tt.setup(t, e)
			}
			orig := crypt.ReadPassphrase
			t.Cleanup(func() { crypt.ReadPassphrase = orig })
			crypt.ReadPassphrase = func(string) ([]byte, error) {
				if v, ok := os.LookupEnv(crypt.EnvAgePassphrase); ok {
					return []byte(v), nil
				}
				t.Error("doctor asked for a passphrase")
				return nil, errors.New("no prompt in tests")
			}

			out, err := runDoctor(t, e.app)
			if got := doctorLine(out, tt.check); got != tt.status {
				t.Errorf("%s line status = %q, want %q\n%s", tt.check, got, tt.status, out)
			}
			if tt.wantErr != errors.Is(err, ErrChecksFailed) {
				t.Errorf("err = %v, wantErr %v\n%s", err, tt.wantErr, out)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected err: %v", err)
			}
			if !strings.HasPrefix(out, "ok   version   test\n") {
				t.Errorf("first line is not the version:\n%s", out)
			}
		})
	}
}

func TestDoctor_NoIdentityCountsEncryptedFiles(t *testing.T) {
	e := doctorFixture(t)
	e.cfg.Encryption.Age.Identity = ""
	e.save(t)
	out, _ := runDoctor(t, e.app)
	if !strings.Contains(out, "apply skips 1 encrypted file(s)") {
		t.Errorf("missing encrypted count:\n%s", out)
	}
}

func TestDoctor_MissingConfig(t *testing.T) {
	a := &App{HomeDir: t.TempDir(), ConfigDir: filepath.Join(t.TempDir(), "dman")}
	out, err := runDoctor(t, a)
	if !errors.Is(err, ErrChecksFailed) {
		t.Fatalf("err = %v, want ErrChecksFailed", err)
	}
	if doctorLine(out, "config") != "fail" || !strings.Contains(out, "dman init <repo-url>") {
		t.Errorf("missing config fail with init hint:\n%s", out)
	}
	if doctorLine(out, "git") == "" {
		t.Errorf("git check missing:\n%s", out)
	}
	if n := strings.Count(strings.TrimSpace(out), "\n") + 1; n != 3 {
		t.Errorf("got %d lines, want version, config and git:\n%s", n, out)
	}
	if isExist(a.ConfigDir) {
		t.Errorf("doctor created %s", a.ConfigDir)
	}
}

func TestDoctor_WritesNothing(t *testing.T) {
	e := doctorFixture(t)
	root := filepath.Dir(e.repo)
	before := pathTree(t, root)
	out, err := runDoctor(t, e.app)
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	after := pathTree(t, root)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Errorf("file tree changed\nbefore: %v\nafter:  %v", before, after)
	}
	if isExist(e.cfg.Snapshots.Path) {
		t.Errorf("doctor created the snapshot store")
	}
}

// pathTree maps every path below root, directories and .git included, to its
// mode, size and modification time.
func pathTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		tree[p] = fmt.Sprintf("%v %d %d", info.Mode(), info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}
