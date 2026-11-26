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
	"github.com/alexjoedt/dman/internal/profile"
	"github.com/alexjoedt/log"
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

// cryptTracked returns the repository-relative path of the first regular file
// <rel>.crypt found in layers of repo, or "" when no layer stores rel
// encrypted. layers are profile names, "" for the root; rel is the
// dot_-encoded path relative to a layer directory.
func cryptTracked(repo string, layers []string, rel string) (string, error) {
	for _, name := range layers {
		p := filepath.Join(profile.Dir(repo, name), rel) + dotfile.CryptSuffix
		fi, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("check %s: %w", p, err)
		}
		if fi.Mode().IsRegular() {
			return filepath.Rel(repo, p)
		}
	}
	return "", nil
}

// repoLayers lists every layer of repo: "" for the root, then each profile.
func repoLayers(repo string) ([]string, error) {
	names, err := profile.List(repo)
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	return append([]string{""}, names...), nil
}

// plainShadow returns the repo-relative .crypt path some layer stores for the
// plain pair p, or "" for an encrypted pair or one nobody stores encrypted.
func (a *App) plainShadow(repo string, layers []string, p dotfile.Pair) (string, error) {
	if p.Encrypted {
		return "", nil
	}
	rel, err := dotfile.TransformPath(a.HomeDir, "", p.Dst)
	if err != nil {
		return "", err
	}
	return cryptTracked(repo, layers, rel)
}

// skipPlainShadow reports whether sync and save must not write p: a plain copy
// beside an encrypted one in another layer would receive the secret as
// plaintext. add converts such a file; sync only refuses.
func (a *App) skipPlainShadow(repo string, layers []string, p dotfile.Pair) (bool, error) {
	tracked, err := a.plainShadow(repo, layers, p)
	if err != nil || tracked == "" {
		return false, err
	}
	log.Warn("skip plain copy of a file stored encrypted in another layer; run: dman add "+tildePath(a.HomeDir, p.Dst),
		"file", p.Src, "encrypted", tracked)
	return true, nil
}

func tildePath(home, p string) string {
	if rel, err := filepath.Rel(home, p); err == nil {
		return "~/" + rel
	}
	return p
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
