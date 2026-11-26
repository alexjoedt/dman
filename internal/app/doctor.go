package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alexjoedt/dman/internal/crypt"
	"github.com/alexjoedt/dman/internal/dotfile"
	"github.com/alexjoedt/dman/internal/profile"
	"github.com/alexjoedt/dman/internal/snapshot"
)

// ErrChecksFailed is returned by Doctor when at least one check failed. The
// report is already printed, so main exits 1 without another message.
var ErrChecksFailed = errors.New("doctor: checks failed")

type report struct{ failed bool }

func (r *report) line(status, check, value, hint string) {
	if status == "fail" {
		r.failed = true
	}
	s := fmt.Sprintf("%-4s %-9s %s", status, check, value)
	if hint != "" {
		s += "  (" + hint + ")"
	}
	fmt.Println(s)
}

func (r *report) err() error {
	if r.failed {
		return ErrChecksFailed
	}
	return nil
}

const hintIdentity = "run: dman config encryption.age.identity <path>"

// Doctor prints one ok/warn/fail line per check of the local setup. It is
// read-only: it creates no directory, takes no snapshot, never prompts and
// never talks to the remote.
func (a *App) Doctor(ctx context.Context, version string) error {
	r := &report{}
	r.line("ok", "version", version, "")

	cfgPath := filepath.Join(a.ConfigDir, configFileName)
	cfg, cfgErr := a.readConfig()
	switch {
	case errors.Is(cfgErr, ErrNoConfig):
		r.line("fail", "config", cfgPath+" not found", "run: dman init <repo-url>")
	case cfgErr != nil:
		r.line("fail", "config", cfgErr.Error(), "fix or remove "+cfgPath+", or run: dman init <repo-url>")
	default:
		r.line("ok", "config", cfgPath, "")
		if legacy := filepath.Join(a.HomeDir, ".config", "dman", configFileName); shadows(legacy, cfgPath) {
			r.line("warn", "legacy", legacy+" is shadowed by "+cfgPath, "run: rm "+legacy)
		}
	}

	if p, err := exec.LookPath("git"); err != nil {
		r.line("fail", "git", "not found in PATH", "install git")
	} else {
		r.line("ok", "git", p, "")
	}
	if cfgErr != nil {
		return r.err()
	}

	var encrypted []dotfile.Pair
	known := a.doctorRepo(ctx, r, cfg)
	if known {
		encrypted, known = a.doctorProfile(r, cfg)
	}
	a.doctorIdentity(r, cfg, encrypted, known)
	a.doctorSnapshots(r, cfg)
	return r.err()
}

// doctorRepo checks the clone and its remote and reports whether the repo is
// usable for the profile checks.
func (a *App) doctorRepo(ctx context.Context, r *report, cfg *Config) bool {
	const hint = "run: dman purge, then dman init <repo-url>"
	if cfg.Path == "" {
		r.line("fail", "repo", "no path in "+filepath.Join(a.ConfigDir, configFileName), hint)
		return false
	}
	if info, err := os.Stat(cfg.Path); err != nil || !info.IsDir() {
		r.line("fail", "repo", cfg.Path+" missing", hint)
		return false
	}
	repo, err := a.getRepo(cfg.Path)
	if err != nil {
		r.line("fail", "repo", err.Error(), hint)
		return false
	}
	if !isDotfileRepo(cfg.Path) {
		r.line("fail", "repo", cfg.Path+" is not a dman repository (no dot_* entries, no profiles/)", hint)
		return false
	}
	r.line("ok", "repo", cfg.Path, "")
	if url, err := repo.RemoteURL(ctx); err != nil {
		r.line("warn", "remote", err.Error(), "check: git -C "+cfg.Path+" remote -v")
	} else if url == "" {
		r.line("warn", "remote", "none", "run: git -C "+cfg.Path+" remote add origin <url>")
	} else {
		r.line("ok", "remote", url, "")
	}
	return true
}

// doctorProfile prints the active profile's chain and returns the encrypted
// pairs of the effective set apply would write, and whether that set is known.
func (a *App) doctorProfile(r *report, cfg *Config) ([]dotfile.Pair, bool) {
	info, err := profile.Resolve(cfg.Path, cfg.Profile)
	if err != nil {
		hint := "fix the profile.json files under " + profile.Root(cfg.Path)
		if errors.Is(err, profile.ErrInheritance) {
			hint = "run: dman profiles inherit <child> <parent> --clear"
		}
		r.line("fail", "profile", err.Error(), hint)
		return nil, false
	}
	var layers []string
	if !info.Standalone || len(info.Root) > 0 {
		layers = append(layers, "root")
	}
	layers = append(layers, info.Chain...)
	value := cfg.Profile
	if value == "" {
		value = "none"
	}
	value += ": " + strings.Join(layers, " -> ")
	if label := standaloneLabel(info.Standalone, info.Root); label != "" {
		value += " (" + label + ")"
	}
	pairs, err := a.collectTracked(cfg, cfg.Profile)
	if err != nil {
		r.line("fail", "profile", err.Error(), "fix the repository layout")
		return nil, false
	}
	if cfg.Profile != "" && !profile.Exists(cfg.Path, cfg.Profile) {
		r.line("warn", "profile", cfg.Profile+": no directory "+profile.Dir(cfg.Path, cfg.Profile)+", apply writes the base files only", "run: dman profiles list")
	} else {
		r.line("ok", "profile", value, "")
	}

	var encrypted []dotfile.Pair
	for _, p := range dotfile.Merge(pairs) {
		if p.Encrypted && !isSymlink(p.Src) && !isSymlink(p.Dst) {
			encrypted = append(encrypted, p)
		}
	}
	return encrypted, true
}

// doctorIdentity checks the age identity and, when that needs no prompt,
// decrypts one encrypted file of the effective set in memory.
func (a *App) doctorIdentity(r *report, cfg *Config, encrypted []dotfile.Pair, known bool) {
	path, source := expandTilde(a.HomeDir, cfg.Encryption.Age.Identity), "config"
	if env := os.Getenv(crypt.EnvAgeIdentity); env != "" {
		path, source = env, crypt.EnvAgeIdentity
	}
	if path == "" {
		value := "none configured"
		if known {
			value += fmt.Sprintf(", apply skips %d encrypted file(s)", len(encrypted))
		}
		r.line("warn", "identity", value, hintIdentity)
		return
	}
	hint := hintIdentity
	if source == crypt.EnvAgeIdentity {
		hint = "fix or unset " + crypt.EnvAgeIdentity
	}
	data, err := os.ReadFile(path)
	if err != nil {
		r.line("fail", "identity", fmt.Sprintf("%s (from %s): %v", path, source, err), hint)
		return
	}
	// A protected identity is only unlocked when the passphrase needs no prompt.
	_, hasPass := os.LookupEnv(crypt.EnvAgePassphrase)
	protected := crypt.IsEncrypted(data)
	var codec *crypt.Codec
	if !protected || hasPass {
		codec, err = crypt.New(crypt.Options{Identity: path})
		if err == nil {
			err = codec.Unlock()
		}
		if err != nil {
			unlockHint := hint
			if protected {
				unlockHint = "check " + crypt.EnvAgePassphrase
			}
			r.line("fail", "identity", fmt.Sprintf("%s (from %s): %v", path, source, err), unlockHint)
			return
		}
	}
	r.line("ok", "identity", fmt.Sprintf("%s (from %s)", path, source), "")

	if !known {
		return
	}
	if len(encrypted) == 0 {
		r.line("ok", "key", "no encrypted files to verify", "")
		return
	}
	if codec == nil {
		r.line("warn", "key", "protected, not verified", "set "+crypt.EnvAgePassphrase+" and run: dman doctor")
		return
	}
	p := encrypted[0]
	if _, err := readTracked(codec, p); err != nil {
		r.line("fail", "key", err.Error(), hint)
		return
	}
	r.line("ok", "key", "decrypted "+p.Src, "")
}

func (a *App) doctorSnapshots(r *report, cfg *Config) {
	if !cfg.Snapshots.Enabled {
		r.line("warn", "snapshots", "disabled", "run: dman config snapshots.enabled true")
		return
	}
	dir := a.snapshotDir(cfg)
	n, err := snapshot.Count(dir)
	if err != nil {
		r.line("fail", "snapshots", fmt.Sprintf("enabled, %s: %v", dir, err), "check the permissions of "+dir)
		return
	}
	r.line("ok", "snapshots", fmt.Sprintf("enabled, %s, %d snapshot(s)", dir, n), "")
	if cfg.Snapshots.Path != "" {
		return
	}
	if legacy := filepath.Join(a.HomeDir, ".local", "state", "dman", "snapshots"); shadows(legacy, dir) {
		r.line("warn", "legacy", legacy+" is shadowed by "+dir, "move what you need, then run: rm -r "+legacy)
	}
}

// shadows reports whether legacy exists and is not the same file as live, so
// a symlinked or differently spelled path to live is never reported.
func shadows(legacy, live string) bool {
	li, err := os.Stat(legacy)
	if err != nil {
		return false
	}
	vi, err := os.Stat(live)
	return err != nil || !os.SameFile(li, vi)
}
