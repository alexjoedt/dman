package dotfile

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestTransformPath(t *testing.T) {
	base := t.TempDir()
	home := "/Users/user"

	tests := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{
			name: "simple dotfile",
			path: "/Users/user/.zshrc",
			want: base + "/dot_zshrc",
		},
		{
			name: "nested dotfile",
			path: "/Users/user/.config/nvim/init.lua",
			want: base + "/dot_config/nvim/init.lua",
		},
		{
			name: "nested dotfile with inner dot",
			path: "/Users/user/.config/.config",
			want: base + "/dot_config/.config",
		},
		{
			name:    "not a dotfile",
			path:    "/Users/user/Downloads",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := TransformPath(home, base, tc.path)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("want %s; got %s", tc.want, got)
			}
		})
	}
}

func TestBackupName(t *testing.T) {
	home := "/Users/user"

	tests := []struct {
		name    string
		dst     string
		wantPfx string
		wantSfx string
	}{
		{
			name:    "simple dotfile",
			dst:     "/Users/user/.zshrc",
			wantPfx: "_zshrc_",
			wantSfx: ".bak",
		},
		{
			name:    "nested dotfile",
			dst:     "/Users/user/.config/nvim/init.lua",
			wantPfx: "_config_nvim_init_",
			wantSfx: ".lua.bak",
		},
		{
			name:    "dotfile without extension",
			dst:     "/Users/user/.bashrc",
			wantPfx: "_bashrc_",
			wantSfx: ".bak",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := backupName(tc.dst, home)
			if len(got) < len(tc.wantPfx)+len(tc.wantSfx) {
				t.Fatalf("backup name too short: %s", got)
			}
			if got[:len(tc.wantPfx)] != tc.wantPfx {
				t.Errorf("prefix: want %q got %q (full: %s)", tc.wantPfx, got[:len(tc.wantPfx)], got)
			}
			if got[len(got)-len(tc.wantSfx):] != tc.wantSfx {
				t.Errorf("suffix: want %q got %q (full: %s)", tc.wantSfx, got[len(got)-len(tc.wantSfx):], got)
			}
		})
	}
}

func TestMergePairs_ProfileOverridesBase(t *testing.T) {
	base := Pair{Src: "/repo/base/dot_zshrc", Dst: "/home/.zshrc"}
	profile := Pair{Src: "/repo/profiles/work/dot_zshrc", Dst: "/home/.zshrc"}

	result := Merge([]Pair{base, profile})

	if len(result) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(result))
	}
	if result[0].Src != profile.Src {
		t.Errorf("expected profile src %s, got %s", profile.Src, result[0].Src)
	}
}

func TestMergePairs_NoConflict(t *testing.T) {
	pairs := []Pair{
		{Src: "/repo/base/dot_zshrc", Dst: "/home/.zshrc"},
		{Src: "/repo/base/dot_vimrc", Dst: "/home/.vimrc"},
	}

	result := Merge(pairs)

	if len(result) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(result))
	}
}

func TestCollect_RootSkipsProfilesAndGit(t *testing.T) {
	repo := t.TempDir()
	home := "/home/user"

	files := []string{
		"dot_zshrc",
		"dot_config/nvim/init.lua",
		"README.md",               // not a dotfile, ignored
		".git/config",             // git internals, skipped
		"profiles/work/dot_zshrc", // profile overlay, skipped at root
	}
	for _, rel := range files {
		p := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	pairs, err := Collect(repo, home, true)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	var dsts []string
	for _, p := range pairs {
		dsts = append(dsts, p.Dst)
	}
	sort.Strings(dsts)

	want := []string{
		filepath.Join(home, ".config/nvim/init.lua"),
		filepath.Join(home, ".zshrc"),
	}
	if len(dsts) != len(want) {
		t.Fatalf("want %v; got %v", want, dsts)
	}
	for i := range want {
		if dsts[i] != want[i] {
			t.Errorf("want %s; got %s", want[i], dsts[i])
		}
	}
}

func TestCollect_ProfileDoesNotSkipProfiles(t *testing.T) {
	profileDir := t.TempDir()
	home := "/home/user"

	p := filepath.Join(profileDir, "dot_zshrc")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	pairs, err := Collect(profileDir, home, false)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(pairs) != 1 || pairs[0].Dst != filepath.Join(home, ".zshrc") {
		t.Fatalf("unexpected pairs: %+v", pairs)
	}
}

func TestFilterPairs(t *testing.T) {
	home := "/home/user"
	pairs := []Pair{
		{Src: "/repo/base/dot_zshrc", Dst: "/home/user/.zshrc"},
		{Src: "/repo/base/dot_vimrc", Dst: "/home/user/.vimrc"},
		{Src: "/repo/base/dot_config/nvim/init.lua", Dst: "/home/user/.config/nvim/init.lua"},
	}

	t.Run("absolute path", func(t *testing.T) {
		got, err := FilterPairs(pairs, home, []string{"/home/user/.zshrc"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].Dst != "/home/user/.zshrc" {
			t.Errorf("unexpected result: %+v", got)
		}
	})

	t.Run("tilde prefix", func(t *testing.T) {
		got, err := FilterPairs(pairs, home, []string{"~/.vimrc"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].Dst != "/home/user/.vimrc" {
			t.Errorf("unexpected result: %+v", got)
		}
	})

	t.Run("bare dotfile name", func(t *testing.T) {
		got, err := FilterPairs(pairs, home, []string{".zshrc"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].Dst != "/home/user/.zshrc" {
			t.Errorf("unexpected result: %+v", got)
		}
	})

	t.Run("multiple targets", func(t *testing.T) {
		got, err := FilterPairs(pairs, home, []string{"~/.zshrc", "~/.vimrc"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("expected 2 pairs, got %d", len(got))
		}
	})

	t.Run("unknown target returns error", func(t *testing.T) {
		_, err := FilterPairs(pairs, home, []string{"~/.bashrc"})
		if err == nil {
			t.Fatal("expected error for unknown target, got nil")
		}
	})
}
