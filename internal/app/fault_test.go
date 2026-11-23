package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
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
