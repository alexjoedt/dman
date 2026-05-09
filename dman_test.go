package dman

import (
	"errors"
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
			got, err := transformPath(home, base, tc.path)
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
		wantPfx string // prefix before the timestamp
		wantSfx string // suffix after the timestamp
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
			wantPfx: "_config_nvim_init.lua_",
			wantSfx: ".bak",
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
	base := filePair{src: "/repo/base/dot_zshrc", dst: "/home/.zshrc"}
	profile := filePair{src: "/repo/profiles/work/dot_zshrc", dst: "/home/.zshrc"}

	result := mergePairs([]filePair{base, profile})

	if len(result) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(result))
	}
	if result[0].src != profile.src {
		t.Errorf("expected profile src %s, got %s", profile.src, result[0].src)
	}
}

func TestMergePairs_NoConflict(t *testing.T) {
	pairs := []filePair{
		{src: "/repo/base/dot_zshrc", dst: "/home/.zshrc"},
		{src: "/repo/base/dot_vimrc", dst: "/home/.vimrc"},
	}

	result := mergePairs(pairs)

	if len(result) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(result))
	}
}

func TestErrNoConfig(t *testing.T) {
	if !errors.Is(ErrNoConfig, ErrNoConfig) {
		t.Error("ErrNoConfig sentinel not comparable with errors.Is")
	}
}
