package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/alexjoedt/dman/internal/crypt"
	"github.com/alexjoedt/dman/internal/dotfile"
	"github.com/alexjoedt/log"
)

// cryptEnv is a repo/home/config triple with an age identity configured.
type cryptEnv struct {
	app      *App
	cfg      *Config
	home     string
	repo     string
	identity string
}

// setupCryptFixture creates the directories, generates an age identity and
// saves a config that points at it. With withKey false the identity file is
// still generated but not configured, which mirrors a machine without the key.
func setupCryptFixture(t *testing.T, withKey bool) *cryptEnv {
	t.Helper()
	t.Setenv(crypt.EnvAgeIdentity, "")
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	home := filepath.Join(dir, "home")
	cfgDir := filepath.Join(dir, "config")
	for _, d := range []string{repo, home, cfgDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	identity := filepath.Join(dir, "key.txt")
	if err := os.WriteFile(identity, []byte(id.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Path: repo, Profile: "default", Encryption: &EncryptionConfig{}}
	if withKey {
		cfg.Encryption.Age.Identity = identity
	}
	a := &App{HomeDir: home, ConfigDir: cfgDir}
	if err := a.saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	return &cryptEnv{app: a, cfg: cfg, home: home, repo: repo, identity: identity}
}

// codec returns a codec for the fixture identity regardless of the config, so
// tests can seed ciphertext even when the app itself has no key.
func (e *cryptEnv) codec(t *testing.T) *crypt.Codec {
	t.Helper()
	c, err := crypt.New(crypt.Options{Identity: e.identity})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (e *cryptEnv) writeHome(t *testing.T, rel string, content string) string {
	t.Helper()
	p := filepath.Join(e.home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *cryptEnv) writeRepo(t *testing.T, rel string, content string) string {
	t.Helper()
	p := filepath.Join(e.repo, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// encryptRepo stores content encrypted at <repo>/rel.
func (e *cryptEnv) encryptRepo(t *testing.T, rel string, content string) string {
	t.Helper()
	ct, err := e.codec(t).Encrypt([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	return e.writeRepo(t, rel, string(ct))
}

// decryptRepo returns the plaintext stored at <repo>/rel.
func (e *cryptEnv) decryptRepo(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(e.repo, rel))
	if err != nil {
		t.Fatal(err)
	}
	if !crypt.IsEncrypted(data) {
		t.Fatalf("%s is not encrypted: %q", rel, data)
	}
	plain, err := e.codec(t).Decrypt(data)
	if err != nil {
		t.Fatal(err)
	}
	return string(plain)
}

// captureLog routes the default logger into a buffer for the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Default()
	log.SetDefault(log.NewCLILogger(log.WithWriter(&buf)))
	t.Cleanup(func() { log.SetDefault(prev) })
	return &buf
}

func readFileString(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// ---- Add ----

func TestAdd_EncryptWritesCryptFile(t *testing.T) {
	e := setupCryptFixture(t, true)
	src := e.writeHome(t, ".netrc", "machine x login y\n")

	if err := e.app.Add(context.Background(), []string{src}, "", true, false, false, false); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if isExist(filepath.Join(e.repo, "dot_netrc")) {
		t.Error("plain dot_netrc was written")
	}
	if got := e.decryptRepo(t, "dot_netrc.crypt"); got != "machine x login y\n" {
		t.Errorf("decrypted = %q", got)
	}
}

func TestAdd_EncryptDirectory(t *testing.T) {
	e := setupCryptFixture(t, true)
	e.writeHome(t, ".ssh/config", "Host a\n")
	e.writeHome(t, ".ssh/known_hosts", "a ssh-ed25519 AAA\n")

	if err := e.app.Add(context.Background(), []string{filepath.Join(e.home, ".ssh")}, "", true, false, false, false); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := e.decryptRepo(t, "dot_ssh/config.crypt"); got != "Host a\n" {
		t.Errorf("config = %q", got)
	}
	if got := e.decryptRepo(t, "dot_ssh/known_hosts.crypt"); got != "a ssh-ed25519 AAA\n" {
		t.Errorf("known_hosts = %q", got)
	}
}

func TestAdd_EncryptedReaddUnchangedIsNoop(t *testing.T) {
	e := setupCryptFixture(t, true)
	src := e.writeHome(t, ".netrc", "same\n")
	dst := e.encryptRepo(t, "dot_netrc.crypt", "same\n")
	before := readFileString(t, dst)

	buf := captureLog(t)
	if err := e.app.Add(context.Background(), []string{src}, "", false, false, false, false); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if after := readFileString(t, dst); after != before {
		t.Error("unchanged plaintext was re-encrypted")
	}
	if !strings.Contains(buf.String(), "nothing changed") {
		t.Errorf("log = %q, want nothing changed", buf.String())
	}
}

func TestAdd_EncryptedReaddChangedReencrypts(t *testing.T) {
	e := setupCryptFixture(t, true)
	src := e.writeHome(t, ".netrc", "new\n")
	e.encryptRepo(t, "dot_netrc.crypt", "old\n")

	buf := captureLog(t)
	// No --encrypt flag: the file stays encrypted anyway.
	if err := e.app.Add(context.Background(), []string{src}, "", false, false, false, false); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := e.decryptRepo(t, "dot_netrc.crypt"); got != "new\n" {
		t.Errorf("decrypted = %q", got)
	}
	if isExist(filepath.Join(e.repo, "dot_netrc")) {
		t.Error("plain twin appeared")
	}
	if !strings.Contains(buf.String(), "update:") {
		t.Errorf("log = %q, want update", buf.String())
	}
}

func TestAdd_TransitionPlainToCrypt(t *testing.T) {
	e := setupCryptFixture(t, true)
	src := e.writeHome(t, ".netrc", "secret\n")
	plain := e.writeRepo(t, "dot_netrc", "secret\n")

	buf := captureLog(t)
	if err := e.app.Add(context.Background(), []string{src}, "", true, false, false, false); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if isExist(plain) {
		t.Error("plain file still exists after --encrypt")
	}
	if got := e.decryptRepo(t, "dot_netrc.crypt"); got != "secret\n" {
		t.Errorf("decrypted = %q", got)
	}
	if !strings.Contains(buf.String(), "encrypt:") {
		t.Errorf("log = %q, want encrypt action", buf.String())
	}
}

func TestAdd_BothPlainAndCryptErrors(t *testing.T) {
	e := setupCryptFixture(t, true)
	src := e.writeHome(t, ".netrc", "x\n")
	e.writeRepo(t, "dot_netrc", "x\n")
	e.encryptRepo(t, "dot_netrc.crypt", "x\n")

	err := e.app.Add(context.Background(), []string{src}, "", false, false, false, false)
	if err == nil || !strings.Contains(err.Error(), "remove one") {
		t.Fatalf("Add err = %v, want conflict", err)
	}
}

func TestAdd_EncryptWithoutKeyErrors(t *testing.T) {
	e := setupCryptFixture(t, false)
	src := e.writeHome(t, ".netrc", "x\n")

	err := e.app.Add(context.Background(), []string{src}, "", true, false, false, false)
	if err == nil || !strings.Contains(err.Error(), crypt.ErrNoKey.Error()) {
		t.Fatalf("Add err = %v, want ErrNoKey", err)
	}
	if isExist(filepath.Join(e.repo, "dot_netrc.crypt")) || isExist(filepath.Join(e.repo, "dot_netrc")) {
		t.Error("something was written without a key")
	}
}

func TestAdd_UpdateEncryptedWithoutKeyErrors(t *testing.T) {
	e := setupCryptFixture(t, false)
	src := e.writeHome(t, ".netrc", "new\n")
	e.encryptRepo(t, "dot_netrc.crypt", "old\n")

	err := e.app.Add(context.Background(), []string{src}, "", false, false, false, false)
	if err == nil || !strings.Contains(err.Error(), "cannot update encrypted") {
		t.Fatalf("Add err = %v, want cannot update encrypted", err)
	}
}

// ---- Apply ----

func TestApply_DecryptsAndWrites0600(t *testing.T) {
	e := setupCryptFixture(t, true)
	initGitRepo(t, e.repo)
	e.encryptRepo(t, "dot_netrc.crypt", "secret\n")
	// An existing world-readable file must end up private.
	dst := e.writeHome(t, ".netrc", "stale\n")

	if err := e.app.Apply(context.Background(), "", false, true, true, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := readFileString(t, dst); got != "secret\n" {
		t.Errorf("home = %q", got)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", fi.Mode().Perm())
	}

	buf := captureLog(t)
	if err := e.app.Apply(context.Background(), "", false, true, true, nil); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if !strings.Contains(buf.String(), "Applied 0 file(s)") {
		t.Errorf("second apply log = %q, want 0 files", buf.String())
	}
}

func TestApply_SkipsEncryptedWithoutKey(t *testing.T) {
	e := setupCryptFixture(t, false)
	initGitRepo(t, e.repo)
	e.encryptRepo(t, "dot_netrc.crypt", "secret\n")
	e.writeRepo(t, "dot_zshrc", "plain\n")

	buf := captureLog(t)
	if err := e.app.Apply(context.Background(), "", false, true, true, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if isExist(filepath.Join(e.home, ".netrc")) {
		t.Error("encrypted file was written without a key")
	}
	if got := readFileString(t, filepath.Join(e.home, ".zshrc")); got != "plain\n" {
		t.Errorf(".zshrc = %q", got)
	}
	if !strings.Contains(buf.String(), "1 encrypted file(s) skipped (no key configured)") {
		t.Errorf("log = %q, want skipped summary", buf.String())
	}
}

func TestApply_DryRunEncrypted(t *testing.T) {
	e := setupCryptFixture(t, true)
	initGitRepo(t, e.repo)
	e.encryptRepo(t, "dot_netrc.crypt", "secret\n")

	if err := e.app.Apply(context.Background(), "", true, true, true, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if isExist(filepath.Join(e.home, ".netrc")) {
		t.Error("dry-run wrote a file")
	}
}

// ---- AddSync ----

func TestAddSync_KeepsEncryptedAndReencrypts(t *testing.T) {
	e := setupCryptFixture(t, true)
	srcDir := filepath.Dir(e.writeHome(t, ".config/foo/secret.conf", "v2\n"))
	e.writeHome(t, ".config/foo/plain.conf", "p\n")
	e.encryptRepo(t, "dot_config/foo/secret.conf.crypt", "v1\n")

	if err := e.app.AddSync(context.Background(), srcDir, "", false, false, false, false, false); err != nil {
		t.Fatalf("AddSync: %v", err)
	}
	if got := e.decryptRepo(t, "dot_config/foo/secret.conf.crypt"); got != "v2\n" {
		t.Errorf("decrypted = %q", got)
	}
	if isExist(filepath.Join(e.repo, "dot_config/foo/secret.conf")) {
		t.Error("plain twin was created")
	}
	if got := readFileString(t, filepath.Join(e.repo, "dot_config/foo/plain.conf")); got != "p\n" {
		t.Errorf("plain.conf = %q", got)
	}
}

func TestAddSync_EncryptFlagEncryptsTreeAndPrunesPlain(t *testing.T) {
	e := setupCryptFixture(t, true)
	srcDir := filepath.Dir(e.writeHome(t, ".config/foo/a.conf", "a\n"))
	e.writeHome(t, ".config/foo/b.conf", "b\n")
	plain := e.writeRepo(t, "dot_config/foo/a.conf", "a\n")

	if err := e.app.AddSync(context.Background(), srcDir, "", true, false, false, false, false); err != nil {
		t.Fatalf("AddSync: %v", err)
	}
	if isExist(plain) {
		t.Error("plain a.conf survived the --encrypt sync")
	}
	if got := e.decryptRepo(t, "dot_config/foo/a.conf.crypt"); got != "a\n" {
		t.Errorf("a = %q", got)
	}
	if got := e.decryptRepo(t, "dot_config/foo/b.conf.crypt"); got != "b\n" {
		t.Errorf("b = %q", got)
	}
}

func TestAddSync_EncryptDoesNotPruneSkippedExecutable(t *testing.T) {
	e := setupCryptFixture(t, true)
	srcDir := filepath.Dir(e.writeHome(t, ".config/foo/a.conf", "a\n"))
	// bin was tracked earlier as a plain file and has since become an ELF
	// binary in home; it must survive a --encrypt sync.
	e.writeHome(t, ".config/foo/bin", "\x7fELF\x00")
	tracked := e.writeRepo(t, "dot_config/foo/bin", "old\n")

	if err := e.app.AddSync(context.Background(), srcDir, "", true, false, false, false, false); err != nil {
		t.Fatalf("AddSync: %v", err)
	}
	if !isExist(tracked) {
		t.Error("skipped executable was pruned from repo")
	}
	if got := e.decryptRepo(t, "dot_config/foo/a.conf.crypt"); got != "a\n" {
		t.Errorf("a = %q", got)
	}
}

func TestApply_FixesModeOfUnchangedDecryptedFile(t *testing.T) {
	e := setupCryptFixture(t, true)
	initGitRepo(t, e.repo)
	e.encryptRepo(t, "dot_netrc.crypt", "secret\n")
	// Same content as the repo, but world-readable (writeHome uses 0644).
	dst := e.writeHome(t, ".netrc", "secret\n")

	if err := e.app.Apply(context.Background(), "", false, true, true, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", fi.Mode().Perm())
	}
}

// ---- Sync / SaveToRepo ----

func TestSync_ReencryptsChangedAndSkipsWithoutKey(t *testing.T) {
	e := setupCryptFixture(t, true)
	e.writeHome(t, ".netrc", "new\n")
	e.encryptRepo(t, "dot_netrc.crypt", "old\n")

	if err := e.app.Sync(context.Background(), "", false, false, false, false); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := e.decryptRepo(t, "dot_netrc.crypt"); got != "new\n" {
		t.Errorf("decrypted = %q", got)
	}

	noKey := setupCryptFixture(t, false)
	noKey.writeHome(t, ".netrc", "new\n")
	dst := noKey.encryptRepo(t, "dot_netrc.crypt", "old\n")
	before := readFileString(t, dst)
	buf := captureLog(t)
	if err := noKey.app.Sync(context.Background(), "", false, false, false, false); err != nil {
		t.Fatalf("Sync without key: %v", err)
	}
	if readFileString(t, dst) != before {
		t.Error("encrypted file was touched without a key")
	}
	if !strings.Contains(buf.String(), "skip encrypted file") {
		t.Errorf("log = %q, want skip warning", buf.String())
	}
}

func TestSaveToRepo_UnchangedEncryptedNotRewritten(t *testing.T) {
	e := setupCryptFixture(t, true)
	e.writeHome(t, ".netrc", "same\n")
	dst := e.encryptRepo(t, "dot_netrc.crypt", "same\n")
	before := readFileString(t, dst)

	if err := e.app.SaveToRepo(context.Background(), "", nil); err != nil {
		t.Fatalf("SaveToRepo: %v", err)
	}
	if readFileString(t, dst) != before {
		t.Error("unchanged plaintext was re-encrypted")
	}

	e.writeHome(t, ".netrc", "changed\n")
	if err := e.app.SaveToRepo(context.Background(), "", nil); err != nil {
		t.Fatalf("SaveToRepo: %v", err)
	}
	if got := e.decryptRepo(t, "dot_netrc.crypt"); got != "changed\n" {
		t.Errorf("decrypted = %q", got)
	}
}

// ---- Diff ----

func TestDiff_Encrypted(t *testing.T) {
	e := setupCryptFixture(t, true)
	e.writeHome(t, ".netrc", "home\n")
	e.encryptRepo(t, "dot_netrc.crypt", "repo\n")

	out := captureStdout(t, func() {
		if err := e.app.Diff(context.Background(), "", nil); err != nil {
			t.Fatalf("Diff: %v", err)
		}
	})
	if !strings.Contains(out, "-home") || !strings.Contains(out, "+repo") {
		t.Errorf("diff does not show plaintext: %q", out)
	}
}

func TestDiff_EncryptedNoKey(t *testing.T) {
	e := setupCryptFixture(t, false)
	e.writeHome(t, ".netrc", "home\n")
	e.encryptRepo(t, "dot_netrc.crypt", "repo\n")

	out := captureStdout(t, func() {
		if err := e.app.Diff(context.Background(), "", nil); err != nil {
			t.Fatalf("Diff: %v", err)
		}
	})
	if !strings.Contains(out, "encrypted, no key configured") {
		t.Errorf("out = %q", out)
	}
	if !strings.Contains(out, "1 encrypted file(s) not compared") {
		t.Errorf("out = %q, want summary", out)
	}
}

// ---- Browse ----

func TestComputeChanged_Encrypted(t *testing.T) {
	e := setupCryptFixture(t, true)
	src := e.encryptRepo(t, "dot_netrc.crypt", "same\n")
	dst := e.writeHome(t, ".netrc", "same\n")
	p := dotfile.Pair{Src: src, Dst: dst, Encrypted: true}

	if computeChanged(&p, e.codec(t)) {
		t.Error("identical plaintext reported as changed")
	}
	e.writeHome(t, ".netrc", "other\n")
	if !computeChanged(&p, e.codec(t)) {
		t.Error("different plaintext reported as unchanged")
	}
	if computeChanged(&p, nil) {
		t.Error("undecryptable pair reported as changed")
	}
}

func TestBrowse_EncryptedPaneAndMarker(t *testing.T) {
	e := setupCryptFixture(t, false)
	src := e.encryptRepo(t, "dot_netrc.crypt", "secret\n")
	pairs := []dotfile.Pair{{Src: src, Dst: filepath.Join(e.home, ".netrc"), Encrypted: true}}

	m := newBrowseModel(context.Background(), e.app, e.cfg, "default", pairs)
	m.width, m.height = 100, 24
	m.resizePanes()

	r := m.current()
	if r == nil {
		t.Fatal("no current row")
	}
	if body := m.paneBody(r); !strings.Contains(body, "no key configured") {
		t.Errorf("paneBody = %q", body)
	}
	if line := m.treeLine(*r, false, 60); !strings.Contains(line, "🔒") {
		t.Errorf("treeLine = %q, want lock marker", line)
	}
	if title := m.previewTitle(); !strings.Contains(title, "(encrypted)") {
		t.Errorf("previewTitle = %q", title)
	}
}

// ---- writeFile ----

func TestWriteFile_AtomicLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "out")
	if err := writeFile(dst, strings.NewReader("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readFileString(t, dst); got != "ok" {
		t.Errorf("content = %q", got)
	}

	// A failing reader must not leave a temp file or touch an existing dst.
	err := writeFile(dst, &failingReader{}, 0o600)
	if err == nil {
		t.Fatal("writeFile with failing reader succeeded")
	}
	if got := readFileString(t, dst); got != "ok" {
		t.Errorf("dst was modified by a failed write: %q", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("leftover files: %v", entries)
	}
}

type failingReader struct{}

func (*failingReader) Read([]byte) (int, error) { return 0, os.ErrInvalid }

func TestAdd_TransitionStagesRemovalInGit(t *testing.T) {
	e := setupCryptFixture(t, true)
	initGitRepo(t, e.repo)
	src := e.writeHome(t, ".netrc", "secret\n")
	e.writeRepo(t, "dot_netrc", "secret\n")
	for _, args := range [][]string{
		{"-C", e.repo, "add", "."},
		{"-C", e.repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// addFlag only: the transition must show up in the index.
	if err := e.app.Add(context.Background(), []string{src}, "", true, true, false, false); err != nil {
		t.Fatalf("Add: %v", err)
	}
	out, err := exec.Command("git", "-C", e.repo, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	status := string(out)
	if !strings.Contains(status, "D  dot_netrc\n") || !strings.Contains(status, "A  dot_netrc.crypt\n") {
		t.Errorf("git status = %q, want staged delete of dot_netrc and add of dot_netrc.crypt", status)
	}
}
