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
	"github.com/alexjoedt/dman/internal/profile"
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
	if err := os.MkdirAll(profile.Dir(e.repo, e.cfg.Profile), 0o755); err != nil {
		t.Fatal(err)
	}
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
		hint    string
		wantErr bool
	}{
		{name: "healthy", check: "key", status: "ok"},
		{name: "no remote", check: "remote", status: "warn", hint: "remote add origin"},
		{name: "profile exists", check: "profile", status: "ok"},
		{
			name: "profile without directory",
			setup: func(t *testing.T, e *cryptEnv) {
				e.cfg.Profile = "gone"
				e.save(t)
			},
			check: "profile", status: "warn", hint: "dman profiles list",
		},
		{
			name: "invalid root entry",
			setup: func(t *testing.T, e *cryptEnv) {
				if err := profile.WriteMeta(e.repo, e.cfg.Profile, profile.Meta{Root: []string{"~"}}); err != nil {
					t.Fatal(err)
				}
			},
			check: "profile", status: "fail", hint: "fix the profile.json files", wantErr: true,
		},
		{
			name: "inheritance cycle",
			setup: func(t *testing.T, e *cryptEnv) {
				if err := profile.WriteMeta(e.repo, e.cfg.Profile, profile.Meta{Inherits: e.cfg.Profile}); err != nil {
					t.Fatal(err)
				}
			},
			check: "profile", status: "fail", hint: "--clear", wantErr: true,
		},
		{
			name: "empty repo path",
			setup: func(t *testing.T, e *cryptEnv) {
				e.cfg.Path = ""
				e.save(t)
			},
			check: "repo", status: "fail", hint: "no path in", wantErr: true,
		},
		{
			name: "identity is not an age key",
			setup: func(t *testing.T, e *cryptEnv) {
				if err := os.WriteFile(e.identity, []byte("Host example\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			check: "identity", status: "fail", wantErr: true,
		},
		{
			name: "wrong key from env",
			setup: func(t *testing.T, e *cryptEnv) {
				id, err := age.GenerateX25519Identity()
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "old.txt")
				if err := os.WriteFile(path, []byte(id.String()+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv(crypt.EnvAgeIdentity, path)
			},
			check: "key", status: "fail", hint: "fix or unset " + crypt.EnvAgeIdentity, wantErr: true,
		},
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
			if tt.hint != "" && !strings.Contains(out, tt.hint) {
				t.Errorf("output lacks hint %q\n%s", tt.hint, out)
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

func TestDoctor_LegacyPaths(t *testing.T) {
	e := doctorFixture(t)
	e.cfg.Snapshots.Path = ""
	e.save(t)
	t.Setenv("XDG_STATE_HOME", "")
	link := func(t *testing.T, target, name string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, name); err != nil {
			t.Fatal(err)
		}
	}

	// The legacy config dir is a symlink to the live one: same file, no warning.
	link(t, e.app.ConfigDir, filepath.Join(e.home, ".config", "dman"))
	out, err := runDoctor(t, e.app)
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	if doctorLine(out, "legacy") != "" {
		t.Errorf("same file reported as legacy:\n%s", out)
	}

	// A real second config is shadowed.
	if err := os.Remove(filepath.Join(e.home, ".config", "dman")); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(e.home, ".config", "dman", configFileName)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _ = runDoctor(t, e.app)
	if !strings.Contains(out, "rm "+legacy) {
		t.Errorf("shadowed legacy config not reported:\n%s", out)
	}
	if err := os.Remove(legacy); err != nil {
		t.Fatal(err)
	}

	// The live snapshot store reached through a symlinked state dir.
	dir := e.app.snapshotDir(e.cfg)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	link(t, filepath.Join(e.home, ".local", "state"), os.Getenv("XDG_STATE_HOME"))
	out, _ = runDoctor(t, e.app)
	if doctorLine(out, "legacy") != "" {
		t.Errorf("live snapshot store reported as legacy:\n%s", out)
	}
}

func TestDoctor_FailsOnPlainCopyOfEncryptedFile(t *testing.T) {
	e := doctorFixture(t)
	if out, err := runDoctor(t, e.app); err != nil || doctorLine(out, "plaintext") != "" {
		t.Fatalf("clean repo: err %v, want no plaintext line:\n%s", err, out)
	}
	e.writeRepo(t, "profiles/other/dot_secret", "leaked")
	out, err := runDoctor(t, e.app)
	if !errors.Is(err, ErrChecksFailed) {
		t.Fatalf("err = %v, want ErrChecksFailed", err)
	}
	if doctorLine(out, "plaintext") != "fail" || strings.Count(out, "plaintext") != 1 {
		t.Errorf("want one plaintext fail line:\n%s", out)
	}
	for _, want := range []string{"profiles/other/dot_secret", "dot_secret.crypt", "dman add ~/.secret --profile other"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	home := e.writeHome(t, ".secret", "rotated")
	if err := e.app.Add(context.Background(), []string{home}, "other", false, false, false, false, false); err != nil {
		t.Fatalf("add: %v", err)
	}
	if out, err := runDoctor(t, e.app); err != nil {
		t.Errorf("after conversion: %v\n%s", err, out)
	}
}
