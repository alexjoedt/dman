package app

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alexjoedt/dman/internal/crypt"
	"github.com/alexjoedt/dman/internal/dotfile"
	"github.com/alexjoedt/dman/internal/hash"
)

// codecCache builds the crypt.Codec once per process. Browse calls Apply and
// SaveToRepo, which re-read the config; without the cache a passphrase-
// protected identity would prompt again on every action.
type codecCache struct {
	mu    sync.Mutex
	done  bool
	codec *crypt.Codec
	err   error
}

// codec returns the codec for cfg or crypt.ErrNoKey when no identity is
// configured.
func (a *App) codec(cfg *Config) (*crypt.Codec, error) {
	a.crypt.mu.Lock()
	defer a.crypt.mu.Unlock()
	if !a.crypt.done {
		var identity string
		if cfg.Encryption != nil {
			identity = expandTilde(a.HomeDir, cfg.Encryption.Age.Identity)
		}
		a.crypt.codec, a.crypt.err = crypt.New(crypt.Options{Identity: identity})
		a.crypt.done = true
	}
	return a.crypt.codec, a.crypt.err
}

// optionalCodec is codec with "no key" mapped to a nil codec. Commands that
// can work without a key (apply, diff, browse, sync) use it and let the nil
// codec surface crypt.ErrNoKey per file.
func (a *App) optionalCodec(cfg *Config) (*crypt.Codec, error) {
	c, err := a.codec(cfg)
	if errors.Is(err, crypt.ErrNoKey) {
		return nil, nil
	}
	return c, err
}

func expandTilde(home, p string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// readTracked returns the plaintext behind a repository file, decrypting it
// when the pair is encrypted.
func readTracked(codec *crypt.Codec, p dotfile.Pair) ([]byte, error) {
	data, err := os.ReadFile(p.Src)
	if err != nil || !p.Encrypted {
		return data, err
	}
	plain, err := codec.Decrypt(data)
	if err != nil {
		return nil, fmt.Errorf("decrypt %s: %w", p.Src, err)
	}
	return plain, nil
}

// plainHash hashes the plaintext of a repository file. Ciphertext is never
// compared: age produces different bytes on every encryption.
func plainHash(codec *crypt.Codec, p dotfile.Pair) (string, error) {
	if !p.Encrypted {
		return hash.GetHash(p.Src)
	}
	plain, err := readTracked(codec, p)
	if err != nil {
		return "", err
	}
	return hash.Sum(plain), nil
}

// storeInRepo writes the home file src into the repository at dst, either as
// a plain copy that keeps its mode or as armored ciphertext.
func storeInRepo(codec *crypt.Codec, dst, src string, encrypt bool) error {
	if !encrypt {
		if err := copyFile(dst, src); err != nil {
			return fmt.Errorf("copy %s: %w", src, err)
		}
		return nil
	}
	plain, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	ct, err := codec.Encrypt(plain)
	if errors.Is(err, crypt.ErrNoKey) {
		return fmt.Errorf("encrypt %s: %w (run: dman config encryption.age.identity <path>)", src, err)
	}
	if err != nil {
		return fmt.Errorf("encrypt %s: %w", src, err)
	}
	// Ciphertext is not secret; the decrypted file gets 0600 on apply.
	return writeFile(dst, bytes.NewReader(ct), 0o644)
}

// resolveRepoDst decides where a home file lands in the repository given its
// plain dot_-encoded path. stale is a plain file to remove after an
// --encrypt transition.
func resolveRepoDst(plain string, encrypt bool) (dst string, encrypted bool, stale string, err error) {
	crypted := plain + dotfile.CryptSuffix
	plainExists, cryptExists := isExist(plain), isExist(crypted)
	if plainExists && cryptExists {
		return "", false, "", fmt.Errorf("both %s and %s exist; remove one", plain, crypted)
	}
	switch {
	case encrypt:
		if plainExists {
			stale = plain
		}
		return crypted, true, stale, nil
	case cryptExists:
		return crypted, true, "", nil
	default:
		return plain, false, "", nil
	}
}

// repoHasEncrypted reports whether any layer of the repository tracks a
// .crypt file.
func repoHasEncrypted(repo string) (bool, error) {
	found := errors.New("found")
	err := filepath.WalkDir(repo, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), dotfile.CryptSuffix) {
			return found
		}
		return nil
	})
	if errors.Is(err, found) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("scan %s for encrypted files: %w", repo, err)
	}
	return false, nil
}
