// Package crypt encrypts and decrypts tracked dotfiles with age.
//
// A repository file with the .crypt suffix holds an ASCII-armored age
// ciphertext. The recipient is derived from the configured identity, so a
// single private key is all that has to be set up. The identity file itself
// may be passphrase-protected (age -p); the passphrase is taken from the
// environment or asked for on the terminal.
package crypt

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"filippo.io/age"
	"filippo.io/age/armor"
	"github.com/charmbracelet/x/term"
)

// ErrNoKey is returned when no identity is configured. Callers treat it as
// "encryption is not set up on this machine" and skip encrypted files.
var ErrNoKey = errors.New("no encryption key configured")

// Environment variables that override the config file.
const (
	EnvAgeIdentity   = "DMAN_AGE_IDENTITY"
	EnvAgePassphrase = "DMAN_AGE_PASSPHRASE"
)

// binaryHeader is the first line of an unarmored age file.
const binaryHeader = "age-encryption.org/v1"

// IsEncrypted reports whether data looks like an age file, armored or not.
func IsEncrypted(data []byte) bool {
	head := bytes.TrimLeft(data, " \t\r\n")
	return bytes.HasPrefix(head, []byte(armor.Header)) || bytes.HasPrefix(head, []byte(binaryHeader))
}

// Options are the encryption settings from the config file, before the
// environment is applied.
type Options struct {
	// Identity is the path to the age identity file. Tilde expansion is the
	// caller's job.
	Identity string
}

// Codec encrypts with the recipient derived from an identity file and
// decrypts with every identity in it. A nil *Codec is valid and reports
// ErrNoKey from every method, so callers can carry "no key" without branching.
type Codec struct {
	path string

	once sync.Once
	ids  []age.Identity
	rcpt age.Recipient
	err  error
}

// New resolves the identity path (environment over config) and returns a
// Codec. It returns ErrNoKey when nothing is configured. A configured but
// missing identity file is reported as an error, not as ErrNoKey: that is a
// misconfiguration the user should see. Parsing the identity, and any
// passphrase prompt, is deferred to first use.
func New(opts Options) (*Codec, error) {
	path := opts.Identity
	if env := os.Getenv(EnvAgeIdentity); env != "" {
		path = env
	}
	if path == "" {
		return nil, ErrNoKey
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("identity file %s: %w", path, err)
	}
	return &Codec{path: path}, nil
}

// Path returns the identity file the codec was built from.
func (c *Codec) Path() string {
	if c == nil {
		return ""
	}
	return c.path
}

// ReadPassphrase asks for the passphrase of a protected identity file. It
// prefers EnvAgePassphrase and otherwise reads from the controlling terminal
// without echo. Tests replace it.
var ReadPassphrase = func(prompt string) ([]byte, error) {
	if p, ok := os.LookupEnv(EnvAgePassphrase); ok {
		return []byte(p), nil
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("identity file is passphrase-protected; set %s or run interactively", EnvAgePassphrase)
	}
	defer func() { _ = tty.Close() }()

	if _, err := fmt.Fprint(tty, prompt); err != nil {
		return nil, err
	}
	pass, err := term.ReadPassword(tty.Fd())
	_, _ = fmt.Fprintln(tty)
	if err != nil {
		return nil, fmt.Errorf("read passphrase: %w", err)
	}
	return pass, nil
}

// Unlock loads the identities now instead of on first use. Browse calls it
// before the TUI takes over the terminal so a passphrase prompt is not drawn
// into the alternate screen.
func (c *Codec) Unlock() error {
	if c == nil {
		return ErrNoKey
	}
	return c.load()
}

func (c *Codec) load() error {
	c.once.Do(func() {
		data, err := os.ReadFile(c.path)
		if err != nil {
			c.err = fmt.Errorf("read identity %s: %w", c.path, err)
			return
		}
		if IsEncrypted(data) {
			data, err = c.unlockIdentity(data)
			if err != nil {
				c.err = err
				return
			}
		}
		ids, err := age.ParseIdentities(bytes.NewReader(data))
		if err != nil {
			c.err = fmt.Errorf("parse identity %s: %w", c.path, err)
			return
		}
		for _, id := range ids {
			if x, ok := id.(*age.X25519Identity); ok {
				c.rcpt = x.Recipient()
				break
			}
		}
		if c.rcpt == nil {
			c.err = fmt.Errorf("no X25519 identity in %s", c.path)
			return
		}
		c.ids = ids
	})
	return c.err
}

// unlockIdentity decrypts a passphrase-protected identity file.
func (c *Codec) unlockIdentity(data []byte) ([]byte, error) {
	pass, err := ReadPassphrase(fmt.Sprintf("Enter passphrase for %s: ", c.path))
	if err != nil {
		return nil, err
	}
	id, err := age.NewScryptIdentity(string(pass))
	if err != nil {
		return nil, err
	}
	plain, err := decrypt(data, id)
	if err != nil {
		return nil, fmt.Errorf("unlock identity %s: %w", c.path, err)
	}
	return plain, nil
}

// Encrypt returns the armored ciphertext of plaintext for the codec's
// recipient. Every call produces different bytes; compare plaintext, never
// ciphertext.
func (c *Codec) Encrypt(plaintext []byte) ([]byte, error) {
	if c == nil {
		return nil, ErrNoKey
	}
	if err := c.load(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	aw := armor.NewWriter(&buf)
	w, err := age.Encrypt(aw, c.rcpt)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	if err := aw.Close(); err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	return buf.Bytes(), nil
}

// Decrypt returns the plaintext of an armored or binary age ciphertext.
func (c *Codec) Decrypt(ciphertext []byte) ([]byte, error) {
	if c == nil {
		return nil, ErrNoKey
	}
	if err := c.load(); err != nil {
		return nil, err
	}
	plain, err := decrypt(ciphertext, c.ids...)
	var noMatch *age.NoIdentityMatchError
	if errors.As(err, &noMatch) {
		return nil, fmt.Errorf("no identity in %s can decrypt this file", c.path)
	}
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plain, nil
}

func decrypt(ciphertext []byte, ids ...age.Identity) ([]byte, error) {
	var r io.Reader = bytes.NewReader(ciphertext)
	if bytes.HasPrefix(bytes.TrimLeft(ciphertext, " \t\r\n"), []byte(armor.Header)) {
		r = armor.NewReader(r)
	}
	out, err := age.Decrypt(r, ids...)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(out)
}
