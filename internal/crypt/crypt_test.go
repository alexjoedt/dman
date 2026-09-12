package crypt

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/armor"
)

// writeIdentity generates a fresh X25519 identity and writes it to dir.
func writeIdentity(t *testing.T, dir string) (*age.X25519Identity, string) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "key.txt")
	if err := os.WriteFile(path, []byte(id.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return id, path
}

func newCodec(t *testing.T, path string) *Codec {
	t.Helper()
	c, err := New(Options{Identity: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestIsEncrypted(t *testing.T) {
	cases := map[string]struct {
		in   string
		want bool
	}{
		"armored":            {armor.Header + "\nabc\n", true},
		"binary":             {"age-encryption.org/v1\n-> X25519", true},
		"leading whitespace": {"\n  " + armor.Header, true},
		"plain":              {"export FOO=bar\n", false},
		"empty":              {"", false},
		"pgp":                {"-----BEGIN PGP MESSAGE-----", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := IsEncrypted([]byte(tc.in)); got != tc.want {
				t.Fatalf("IsEncrypted = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCodec_RoundTrip(t *testing.T) {
	_, path := writeIdentity(t, t.TempDir())
	c := newCodec(t, path)

	plain := []byte("secret config\n")
	ct1, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !strings.HasPrefix(string(ct1), armor.Header) {
		t.Fatalf("ciphertext is not armored: %q", ct1[:40])
	}
	if !IsEncrypted(ct1) {
		t.Fatal("IsEncrypted(ciphertext) = false")
	}

	got, err := c.Decrypt(ct1)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("Decrypt = %q, want %q", got, plain)
	}

	// Fresh nonce per encryption: ciphertext is never comparable.
	ct2, err := c.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(ct1, ct2) {
		t.Fatal("two encryptions produced identical ciphertext")
	}
}

func TestCodec_DecryptBinary(t *testing.T) {
	id, path := writeIdentity(t, t.TempDir())
	c := newCodec(t, path)

	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, id.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("binary"))
	_ = w.Close()

	got, err := c.Decrypt(buf.Bytes())
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != "binary" {
		t.Fatalf("Decrypt = %q", got)
	}
}

func TestCodec_NoMatchingIdentity(t *testing.T) {
	_, pathA := writeIdentity(t, t.TempDir())
	a := newCodec(t, pathA)
	ct, err := a.Encrypt([]byte("x"))
	if err != nil {
		t.Fatal(err)
	}

	_, pathB := writeIdentity(t, t.TempDir())
	b := newCodec(t, pathB)
	_, err = b.Decrypt(ct)
	if err == nil || !strings.Contains(err.Error(), "no identity in "+pathB) {
		t.Fatalf("Decrypt error = %v, want no-identity error naming %s", err, pathB)
	}
}

// protectIdentity encrypts the identity file at path with a passphrase, the
// way `age -p` does, and returns the path of the protected copy.
func protectIdentity(t *testing.T, path, pass string) string {
	t.Helper()
	plain, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := age.NewScryptRecipient(pass)
	if err != nil {
		t.Fatal(err)
	}
	r.SetWorkFactor(10) // keep the test fast
	var buf bytes.Buffer
	aw := armor.NewWriter(&buf)
	w, err := age.Encrypt(aw, r)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(plain)
	_ = w.Close()
	_ = aw.Close()
	out := path + ".age"
	if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCodec_PassphraseIdentity_Prompt(t *testing.T) {
	_, path := writeIdentity(t, t.TempDir())
	protected := protectIdentity(t, path, "pw")

	orig := ReadPassphrase
	t.Cleanup(func() { ReadPassphrase = orig })
	calls := 0
	ReadPassphrase = func(prompt string) ([]byte, error) {
		calls++
		if !strings.Contains(prompt, protected) {
			t.Errorf("prompt %q does not name the identity file", prompt)
		}
		return []byte("pw"), nil
	}

	c := newCodec(t, protected)
	ct, err := c.Encrypt([]byte("s"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := c.Decrypt(ct); err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if calls != 1 {
		t.Fatalf("passphrase asked %d times, want once", calls)
	}
}

func TestCodec_PassphraseIdentity_Env(t *testing.T) {
	_, path := writeIdentity(t, t.TempDir())
	protected := protectIdentity(t, path, "pw")
	t.Setenv(EnvAgePassphrase, "pw")

	c := newCodec(t, protected)
	if err := c.Unlock(); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
}

func TestCodec_PassphraseIdentity_Wrong(t *testing.T) {
	_, path := writeIdentity(t, t.TempDir())
	protected := protectIdentity(t, path, "pw")
	t.Setenv(EnvAgePassphrase, "nope")

	c := newCodec(t, protected)
	if err := c.Unlock(); err == nil {
		t.Fatal("Unlock with wrong passphrase succeeded")
	}
}

func TestNew(t *testing.T) {
	t.Run("no identity", func(t *testing.T) {
		t.Setenv(EnvAgeIdentity, "")
		_, err := New(Options{})
		if !errors.Is(err, ErrNoKey) {
			t.Fatalf("err = %v, want ErrNoKey", err)
		}
	})
	t.Run("missing file is an error", func(t *testing.T) {
		t.Setenv(EnvAgeIdentity, "")
		_, err := New(Options{Identity: filepath.Join(t.TempDir(), "nope")})
		if err == nil || errors.Is(err, ErrNoKey) {
			t.Fatalf("err = %v, want a stat error", err)
		}
	})
	t.Run("env overrides config", func(t *testing.T) {
		_, path := writeIdentity(t, t.TempDir())
		t.Setenv(EnvAgeIdentity, path)
		c, err := New(Options{Identity: "/does/not/exist"})
		if err != nil {
			t.Fatal(err)
		}
		if c.Path() != path {
			t.Fatalf("Path = %s, want %s", c.Path(), path)
		}
	})
}

func TestNilCodec(t *testing.T) {
	var c *Codec
	if _, err := c.Encrypt([]byte("x")); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Encrypt err = %v", err)
	}
	if _, err := c.Decrypt([]byte("x")); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Decrypt err = %v", err)
	}
	if err := c.Unlock(); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Unlock err = %v", err)
	}
	if c.Path() != "" {
		t.Fatal("nil codec has a path")
	}
}
