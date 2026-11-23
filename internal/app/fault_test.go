package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/alexjoedt/dman/internal/crypt"
	"github.com/alexjoedt/dman/internal/fsutil"
	"github.com/alexjoedt/dman/internal/profile"
	"github.com/alexjoedt/dman/internal/snapshot"
)

var errInjected = errors.New("injected fault")

// faultStep is one write step the fsutil.Fault seam saw.
type faultStep struct {
	k        int
	op, path string
}

// faultMatrix runs scenario once with no fault to count the write steps N of
// its command, then once per k in 1..N in a subtest with step k failing. Each
// scenario call builds a fresh fixture, runs the command through run and
// asserts on the outcome. run returns the injected step (k 0 on the counting
// run) and the command's error. Only the k-th step fails; later steps such as
// a rollback run fault-free. Tests using it must not call t.Parallel.
func faultMatrix(t *testing.T, scenario func(t *testing.T, run func(cmd func() error) (faultStep, error))) int {
	t.Helper()
	t.Cleanup(func() { fsutil.Fault = nil })

	runK := func(t *testing.T, k int, cmd func() error) (faultStep, error) {
		t.Helper()
		var n int
		var hit faultStep
		fsutil.Fault = func(op, path string) error {
			n++
			if n != k {
				return nil
			}
			hit = faultStep{k: k, op: op, path: path}
			return fmt.Errorf("%w at step %d: %s %s", errInjected, k, op, path)
		}
		err := cmd()
		fsutil.Fault = nil
		if k == 0 {
			hit.k = n
		} else if hit.k == 0 {
			t.Fatalf("step %d never reached, command took %d step(s)", k, n)
		}
		return hit, err
	}

	var steps int
	if !t.Run("no fault", func(t *testing.T) {
		scenario(t, func(cmd func() error) (faultStep, error) {
			s, err := runK(t, 0, cmd)
			steps = s.k
			return faultStep{}, err
		})
	}) {
		t.FailNow()
	}
	if steps == 0 {
		t.Fatal("command took no write steps")
	}
	for k := 1; k <= steps; k++ {
		t.Run(fmt.Sprintf("step %d", k), func(t *testing.T) {
			scenario(t, func(cmd func() error) (faultStep, error) {
				return runK(t, k, cmd)
			})
		})
	}
	return steps
}

func TestFaultMatrixHitsEverySeamStepOfApply(t *testing.T) {
	var got []faultStep
	n := faultMatrix(t, func(t *testing.T, run func(func() error) (faultStep, error)) {
		a := snapshotEnv(t)
		cfg, err := a.readConfig()
		if err != nil {
			t.Fatal(err)
		}
		initGitRepo(t, cfg.Path)
		for name, content := range map[string]string{"dot_a": "new a\n", "dot_b": "new b\n"} {
			if err := os.WriteFile(filepath.Join(cfg.Path, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		writeHome(t, a, ".a", "old a\n", 0o644)

		step, err := run(func() error {
			return a.Apply(context.Background(), "", false, true, false, nil)
		})
		if step.k == 0 {
			if err != nil {
				t.Fatalf("apply without fault: %v", err)
			}
			return
		}
		got = append(got, step)
		if !errors.Is(err, errInjected) || !strings.Contains(err.Error(), step.path) {
			t.Errorf("err = %v; want the injected fault on %s", err, step.path)
		}
		if got, err := os.ReadFile(filepath.Join(a.HomeDir, ".a")); err != nil || string(got) != "old a\n" {
			t.Errorf(".a = %q, %v; want %q", got, err, "old a\n")
		}
		if _, err := os.Lstat(filepath.Join(a.HomeDir, ".b")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf(".b: Lstat err = %v; want it absent", err)
		}
	})

	// Snapshot manifest and index, then .a and .b in home.
	if n != 4 || len(got) != 4 {
		t.Fatalf("steps = %d, fault runs = %d; want 4 each", n, len(got))
	}
	for i, want := range []string{".json", "index.json", ".a", ".b"} {
		if got[i].k != i+1 || got[i].op != "rename" || !strings.HasSuffix(got[i].path, want) {
			t.Errorf("step %d = %+v, want rename of *%s", i+1, got[i], want)
		}
	}
}

// crashFixture is a snapshotEnv with an age key, a profile.json and one prior
// snapshot. Apply changes .a, creates .b and rewrites the encrypted .secret.
// It returns the fixture root, which holds home, repo, config and the
// snapshot store.
func crashFixture(t *testing.T) (a *App, root string) {
	t.Helper()
	t.Setenv(crypt.EnvAgeIdentity, "")
	a = snapshotEnv(t)
	root = filepath.Dir(a.HomeDir)
	cfg, err := a.readConfig()
	if err != nil {
		t.Fatal(err)
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(root, "key.txt")
	if err := os.WriteFile(key, []byte(id.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Encryption = &EncryptionConfig{Age: AgeConfig{Identity: key}}
	if err := a.saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	codec, err := crypt.New(crypt.Options{Identity: key})
	if err != nil {
		t.Fatal(err)
	}
	ct, err := codec.Encrypt([]byte("new secret\n"))
	if err != nil {
		t.Fatal(err)
	}

	initGitRepo(t, cfg.Path)
	if err := profile.WriteMeta(cfg.Path, cfg.Profile, profile.Meta{}); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"dot_a": "new a\n", "dot_b": "new b\n", "dot_secret.crypt": string(ct)} {
		if err := os.WriteFile(filepath.Join(cfg.Path, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeHome(t, a, ".a", "old a\n", 0o644)
	writeHome(t, a, ".secret", "old secret\n", 0o644)
	snap(t, a, ".a")
	return a, root
}

// fileTree maps every regular file under root to its mode and content,
// skipping .git and the directories in skip.
func fileTree(t *testing.T, root string, skip ...string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || slices.Contains(skip, p) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		tree[rel] = fmt.Sprintf("%v %q", info.Mode(), data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func assertTree(t *testing.T, name string, got, want map[string]string) {
	t.Helper()
	for p, w := range want {
		if g, ok := got[p]; !ok {
			t.Errorf("%s: %s missing, want %s", name, p, w)
		} else if g != w {
			t.Errorf("%s: %s = %s, want %s", name, p, g, w)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("%s: unexpected %s", name, p)
		}
	}
}

// assertNoTempTree fails for any .dman-* temp file left anywhere under root.
func assertNoTempTree(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".dman-") {
			t.Errorf("temp file left: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// assertStore checks the snapshot store at dir: index.json decodes, every
// listed snapshot has a manifest that decodes, every manifest is listed, and
// the manifests of before are unchanged.
func assertStore(t *testing.T, dir string, before map[string]string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var idx snapshot.Index
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	listed := map[string]bool{}
	for _, m := range idx.Snapshots {
		listed[m.ID+".json"] = true
		data, err := os.ReadFile(filepath.Join(dir, m.ID+".json"))
		if err != nil {
			t.Errorf("listed snapshot %s: %v", m.ID, err)
			continue
		}
		var man snapshot.Manifest
		if err := json.Unmarshal(data, &man); err != nil || man.ID != m.ID {
			t.Errorf("manifest %s: id %q, err %v", m.ID, man.ID, err)
		}
	}
	after := fileTree(t, dir, filepath.Join(dir, "blobs"))
	for name := range after {
		if name != "index.json" && !listed[name] {
			t.Errorf("manifest %s not listed in the index", name)
		}
	}
	for name, content := range before {
		if name != "index.json" && after[name] != content {
			t.Errorf("manifest %s changed or removed", name)
		}
	}
}

func TestApplyFaultLeavesOldOrNewState(t *testing.T) {
	n := faultMatrix(t, func(t *testing.T, run func(func() error) (faultStep, error)) {
		a, root := crashFixture(t)
		store := filepath.Join(root, "snapshots")
		blobs := filepath.Join(store, "blobs")
		before := fileTree(t, root, store)
		storeBefore := fileTree(t, store, blobs)
		blobsBefore := fileTree(t, blobs)

		step, err := run(func() error {
			return a.Apply(context.Background(), "", false, true, false, nil)
		})
		assertNoTempTree(t, root)
		assertStore(t, store, storeBefore)
		if step.k == 0 {
			if err != nil {
				t.Fatalf("apply without fault: %v", err)
			}
			home, err := filepath.Rel(root, a.HomeDir)
			if err != nil {
				t.Fatal(err)
			}
			want := maps.Clone(before)
			for name, f := range map[string]struct {
				mode    fs.FileMode
				content string
			}{".a": {0o644, "new a\n"}, ".b": {0o644, "new b\n"}, ".secret": {0o600, "new secret\n"}} {
				want[filepath.Join(home, name)] = fmt.Sprintf("%v %q", f.mode, f.content)
			}
			assertTree(t, "after apply", fileTree(t, root, store), want)
			return
		}
		if !errors.Is(err, errInjected) {
			t.Errorf("err = %v; want the injected fault", err)
		}
		assertTree(t, "after "+step.op+" "+step.path, fileTree(t, root, store), before)
		// A fault in the store fails the snapshot, which must drop its blobs;
		// a later fault leaves the snapshot listed with its blobs.
		if strings.HasPrefix(step.path, store) {
			assertTree(t, "blobs", fileTree(t, blobs), blobsBefore)
		}
	})
	// Manifest, index.json, then .a, .b and .secret in home.
	if n != 5 {
		t.Errorf("steps = %d, want 5", n)
	}
}

func TestSnapshotCreateFaultLeavesConsistentStore(t *testing.T) {
	n := faultMatrix(t, func(t *testing.T, run func(func() error) (faultStep, error)) {
		a, root := crashFixture(t)
		store := filepath.Join(root, "snapshots")
		blobs := filepath.Join(store, "blobs")
		before := fileTree(t, root, store)
		storeBefore := fileTree(t, store, blobs)
		blobsBefore := fileTree(t, blobs)

		step, err := run(func() error {
			return a.SnapshotCreate(context.Background(), "test")
		})
		assertNoTempTree(t, root)
		assertStore(t, store, storeBefore)
		assertTree(t, "outside the store", fileTree(t, root, store), before)
		if step.k == 0 {
			if err != nil {
				t.Fatalf("snapshot create without fault: %v", err)
			}
			return
		}
		if !errors.Is(err, errInjected) {
			t.Errorf("err = %v; want the injected fault", err)
		}
		if got := len(listSnapshots(t, a)); got != 1 {
			t.Errorf("snapshots = %d, want only the prior one", got)
		}
		assertTree(t, "blobs", fileTree(t, blobs), blobsBefore)
	})
	// Manifest, then index.json.
	if n != 2 {
		t.Errorf("steps = %d, want 2", n)
	}
}

// syncFixture is a crashFixture whose home differs from the repo in three
// tracked files, one of them encrypted. It returns the repo paths sync
// rewrites, relative to root, mapped to the plaintext home content.
func syncFixture(t *testing.T) (a *App, root string, home map[string]string) {
	t.Helper()
	a, root = crashFixture(t)
	cfg, err := a.readConfig()
	if err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.Rel(root, cfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	home = map[string]string{}
	for name, src := range map[string]string{".a": "dot_a", ".b": "dot_b", ".secret": "dot_secret.crypt"} {
		content := "home" + name + "\n"
		writeHome(t, a, name, content, 0o644)
		home[filepath.Join(repo, src)] = content
	}
	return a, root, home
}

// assertRepoFile checks that the repo file rel under root holds its old
// content from before or the home content plain, decrypting a .crypt file.
func assertRepoFile(t *testing.T, root, rel, before, plain string) (written bool) {
	t.Helper()
	info, err := os.Stat(filepath.Join(root, rel))
	if err != nil {
		t.Errorf("%s: %v", rel, err)
		return false
	}
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Errorf("%s: %v", rel, err)
		return false
	}
	if len(data) == 0 {
		t.Errorf("%s is empty", rel)
	}
	if got := fmt.Sprintf("%v %q", info.Mode(), data); got == before {
		return false
	}
	if strings.HasSuffix(rel, ".crypt") {
		codec, err := crypt.New(crypt.Options{Identity: filepath.Join(root, "key.txt")})
		if err != nil {
			t.Fatal(err)
		}
		if data, err = codec.Decrypt(data); err != nil {
			t.Errorf("%s: decrypt: %v", rel, err)
			return false
		}
	}
	if string(data) != plain {
		t.Errorf("%s = %q, want its old content or %q", rel, data, plain)
	}
	return true
}

func TestSyncFaultLeavesEachRepoFileOldOrNew(t *testing.T) {
	n := faultMatrix(t, func(t *testing.T, run func(func() error) (faultStep, error)) {
		a, root, home := syncFixture(t)
		before := fileTree(t, root)

		step, err := run(func() error {
			return a.Sync(context.Background(), "", false, false, false, false)
		})
		assertNoTempTree(t, root)
		if step.k == 0 && err != nil {
			t.Fatalf("sync without fault: %v", err)
		}
		if step.k != 0 && !errors.Is(err, errInjected) {
			t.Errorf("err = %v; want the injected fault", err)
		}

		after := fileTree(t, root)
		var written int
		for rel, plain := range home {
			w := assertRepoFile(t, root, rel, before[rel], plain)
			if w {
				written++
			}
			if w && filepath.Join(root, rel) == step.path {
				t.Errorf("%s written although its rename failed", rel)
			}
			delete(after, rel)
			delete(before, rel)
		}
		if want := len(home); step.k == 0 && written != want {
			t.Errorf("written = %d, want %d", written, want)
		}
		if step.k != 0 && written != step.k-1 {
			t.Errorf("written = %d, want the %d file(s) before the fault", written, step.k-1)
		}
		// Home, dman.json, profile.json and the snapshot store stay as they were.
		assertTree(t, "outside the synced files", after, before)
	})
	if n != 3 {
		t.Errorf("steps = %d, want 3", n)
	}
}

func TestSyncCancelStopsBetweenFiles(t *testing.T) {
	// Cancel during write k: no later write, and no git add or commit, also
	// when k is the last changed file.
	for _, k := range []int{1, 3} {
		t.Run(fmt.Sprint(k), func(t *testing.T) {
			a, root, home := syncFixture(t)
			before := fileTree(t, root)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			t.Cleanup(func() { fsutil.Fault = nil })
			calls := 0
			fsutil.Fault = func(string, string) error {
				if calls++; calls == k {
					cancel()
				}
				return nil
			}

			err := a.Sync(ctx, "", false, false, true, false)
			if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), fmt.Sprintf("%d file(s) already written", k)) {
				t.Fatalf("err = %v; want the cancellation after %d file(s)", err, k)
			}
			after := fileTree(t, root)
			var written int
			for rel, plain := range home {
				if assertRepoFile(t, root, rel, before[rel], plain) {
					written++
				}
				delete(after, rel)
				delete(before, rel)
			}
			if written != k {
				t.Errorf("written = %d, want %d", written, k)
			}
			assertNoTempTree(t, root)
			assertTree(t, "outside the synced files", after, before)

			repo := filepath.Join(root, "repo")
			if out, err := exec.Command("git", "-C", repo, "ls-files").Output(); err != nil || len(out) > 0 {
				t.Errorf("git ls-files = %q, %v; want nothing staged", out, err)
			}
			if exec.Command("git", "-C", repo, "rev-parse", "-q", "--verify", "HEAD").Run() == nil {
				t.Error("HEAD exists; want no commit after cancellation")
			}
		})
	}
}

func TestSyncDryRunStopsOnCancel(t *testing.T) {
	a, root, _ := syncFixture(t)
	before := fileTree(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := a.Sync(ctx, "", true, false, false, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want the cancellation", err)
	}
	assertTree(t, "after dry run", fileTree(t, root), before)
}

// addFixture is a crashFixture with the plain repo file dot_a. Adding its
// home files with --encrypt creates dot_new.crypt, converts dot_a to
// dot_a.crypt and updates dot_secret.crypt, in that order. It returns the
// home files to add, the repo relative to root, and the repo paths, relative
// to root, mapped to their plaintext home content.
func addFixture(t *testing.T) (a *App, root string, files []string, repo string, want map[string]string) {
	t.Helper()
	a, root = crashFixture(t)
	cfg, err := a.readConfig()
	if err != nil {
		t.Fatal(err)
	}
	repo, err = filepath.Rel(root, cfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	want = map[string]string{}
	for _, f := range []struct{ name, dst string }{
		{".new", "dot_new.crypt"}, {".a", "dot_a.crypt"}, {".secret", "dot_secret.crypt"},
	} {
		content := "home" + f.name + "\n"
		files = append(files, writeHome(t, a, f.name, content, 0o644))
		want[filepath.Join(repo, f.dst)] = content
	}
	return a, root, files, repo, want
}

// assertAddedFiles checks that every repo file of want is absent or old, or
// holds its new content, that dot_a is plain or converted but never both, and
// that nothing else under root changed. It returns the number of files
// written.
func assertAddedFiles(t *testing.T, root, repo string, before, want map[string]string) int {
	t.Helper()
	after := fileTree(t, root)
	if _, converted := after[filepath.Join(repo, "dot_a.crypt")]; converted {
		delete(before, filepath.Join(repo, "dot_a"))
	}
	written := 0
	for rel, plain := range want {
		_, existed := before[rel]
		if _, ok := after[rel]; ok || existed {
			if assertRepoFile(t, root, rel, before[rel], plain) {
				written++
			}
		}
		delete(after, rel)
		delete(before, rel)
	}
	assertTree(t, "outside the added files", after, before)
	return written
}

func TestAddFaultLeavesEachRepoFileOldOrNew(t *testing.T) {
	n := faultMatrix(t, func(t *testing.T, run func(func() error) (faultStep, error)) {
		a, root, files, repo, want := addFixture(t)
		before := fileTree(t, root)

		step, err := run(func() error {
			return a.Add(context.Background(), files, "", false, true, false, false, false)
		})
		assertNoTempTree(t, root)
		if step.k == 0 && err != nil {
			t.Fatalf("add without fault: %v", err)
		}
		if step.k != 0 && (!errors.Is(err, errInjected) || !strings.Contains(err.Error(), step.path)) {
			t.Errorf("err = %v; want the injected fault on %s", err, step.path)
		}
		// dot_new.crypt, dot_a.crypt, removal of dot_a, dot_secret.crypt.
		if got, want := assertAddedFiles(t, root, repo, before, want), []int{3, 0, 1, 1, 2}[step.k]; got != want {
			t.Errorf("written = %d, want %d", got, want)
		}
		if step.op == "remove" {
			if _, err := os.Stat(filepath.Join(root, repo, "dot_a.crypt")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("dot_a.crypt: Stat err = %v; want it removed after the failed plain removal", err)
			}
		}
	})
	if n != 4 {
		t.Errorf("steps = %d, want 4", n)
	}
}

func TestAddCancelStopsBetweenFiles(t *testing.T) {
	// Cancel during write k: the conversion of dot_a (steps 2 and 3) is never
	// split, no later file is written and git does not run.
	for _, tc := range []struct{ k, written int }{{2, 2}, {4, 3}} {
		t.Run(fmt.Sprint(tc.k), func(t *testing.T) {
			a, root, files, repo, want := addFixture(t)
			before := fileTree(t, root)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			t.Cleanup(func() { fsutil.Fault = nil })
			calls := 0
			fsutil.Fault = func(string, string) error {
				if calls++; calls == tc.k {
					cancel()
				}
				return nil
			}

			err := a.Add(ctx, files, "", false, true, false, true, false)
			if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), fmt.Sprintf("%d file(s) already written", tc.written)) {
				t.Fatalf("err = %v; want the cancellation after %d file(s)", err, tc.written)
			}
			assertNoTempTree(t, root)
			if got := assertAddedFiles(t, root, repo, before, want); got != tc.written {
				t.Errorf("written = %d, want %d", got, tc.written)
			}
			if _, err := os.Stat(filepath.Join(root, repo, "dot_a")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("dot_a: Stat err = %v; want it converted", err)
			}

			repo = filepath.Join(root, repo)
			if out, err := exec.Command("git", "-C", repo, "ls-files").Output(); err != nil || len(out) > 0 {
				t.Errorf("git ls-files = %q, %v; want nothing staged", out, err)
			}
			if exec.Command("git", "-C", repo, "rev-parse", "-q", "--verify", "HEAD").Run() == nil {
				t.Error("HEAD exists; want no commit after cancellation")
			}
		})
	}
}

func TestAddFailedTwinCleanupReportsBothFiles(t *testing.T) {
	a, root, files, repo, _ := addFixture(t)
	t.Cleanup(func() { fsutil.Fault = nil })
	fsutil.Fault = func(op, path string) error {
		if op == "remove" {
			return fmt.Errorf("%w: %s", errInjected, path)
		}
		return nil
	}

	err := a.Add(context.Background(), files, "", false, true, false, false, false)
	plain, enc := filepath.Join(root, repo, "dot_a"), filepath.Join(root, repo, "dot_a.crypt")
	if !errors.Is(err, errInjected) || !strings.Contains(err.Error(), "remove plain "+plain) || !strings.Contains(err.Error(), "remove "+enc) {
		t.Fatalf("err = %v; want the failed removal of %s and of %s", err, plain, enc)
	}
	if !strings.Contains(err.Error(), "1 file(s) already written to the repo but not staged") {
		t.Errorf("err = %v; want dot_new.crypt named as written but not staged", err)
	}
}
