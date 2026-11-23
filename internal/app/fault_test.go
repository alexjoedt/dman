package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexjoedt/dman/internal/fsutil"
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
