package profile

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// mkProfile creates profiles/<name> in repo and, when parent is non-empty, a
// profile.json declaring it.
func mkProfile(t *testing.T, repo, name, parent string) {
	t.Helper()
	if err := os.MkdirAll(Dir(repo, name), 0o755); err != nil {
		t.Fatal(err)
	}
	if parent != "" {
		if err := WriteMeta(repo, name, Meta{Inherits: parent}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDir(t *testing.T) {
	repo := "/repo"
	tests := []struct {
		name string
		want string
	}{
		{"", repo},
		{"default", filepath.Join(repo, "profiles", "default")},
		{"laptop", filepath.Join(repo, "profiles", "laptop")},
	}
	for _, tt := range tests {
		if got := Dir(repo, tt.name); got != tt.want {
			t.Errorf("Dir(%q) = %q; want %q", tt.name, got, tt.want)
		}
	}
}

func TestChain(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(repo string)
		profile string
		want    []string
		wantErr string
	}{
		{
			name:    "empty name",
			setup:   func(string) {},
			profile: "",
			want:    nil,
		},
		{
			name:    "leaf without directory",
			setup:   func(string) {},
			profile: "ghost",
			want:    []string{"ghost"},
		},
		{
			name: "no meta",
			setup: func(repo string) {
				mkProfile(t, repo, "arch", "")
			},
			profile: "arch",
			want:    []string{"arch"},
		},
		{
			name: "one level",
			setup: func(repo string) {
				mkProfile(t, repo, "arch", "")
				mkProfile(t, repo, "arch-gridx", "arch")
			},
			profile: "arch-gridx",
			want:    []string{"arch", "arch-gridx"},
		},
		{
			name: "three levels",
			setup: func(repo string) {
				mkProfile(t, repo, "linux", "")
				mkProfile(t, repo, "arch", "linux")
				mkProfile(t, repo, "arch-gridx", "arch")
			},
			profile: "arch-gridx",
			want:    []string{"linux", "arch", "arch-gridx"},
		},
		{
			name: "missing parent",
			setup: func(repo string) {
				mkProfile(t, repo, "arch-gridx", "arch")
			},
			profile: "arch-gridx",
			wantErr: `profile "arch-gridx" inherits "arch" which does not exist`,
		},
		{
			name: "self cycle",
			setup: func(repo string) {
				mkProfile(t, repo, "arch", "arch")
			},
			profile: "arch",
			wantErr: "profile inheritance cycle: arch -> arch",
		},
		{
			name: "two node cycle",
			setup: func(repo string) {
				mkProfile(t, repo, "a", "b")
				mkProfile(t, repo, "b", "a")
			},
			profile: "a",
			wantErr: "profile inheritance cycle: a -> b -> a",
		},
		{
			name: "malformed meta",
			setup: func(repo string) {
				mkProfile(t, repo, "arch", "")
				if err := os.WriteFile(filepath.Join(Dir(repo, "arch"), metaFile), []byte("{"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			profile: "arch",
			wantErr: "parse profile.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := t.TempDir()
			tt.setup(repo)
			got, err := Chain(repo, tt.profile)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v; want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Chain: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Chain = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestParents(t *testing.T) {
	repo := t.TempDir()
	mkProfile(t, repo, "linux", "")
	mkProfile(t, repo, "arch", "linux")
	mkProfile(t, repo, "arch-gridx", "arch")

	got, err := Parents(repo, "arch-gridx")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"arch", "linux"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Parents = %v; want %v", got, want)
	}
	got, err = Parents(repo, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("Parents(linux) = %v; want nil", got)
	}
}

func TestReadMeta_Missing(t *testing.T) {
	repo := t.TempDir()
	mkProfile(t, repo, "arch", "")
	m, err := ReadMeta(repo, "arch")
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsZero() {
		t.Errorf("want zero Meta, got %+v", m)
	}
}

func TestWriteMeta_RoundTripAndRemove(t *testing.T) {
	repo := t.TempDir()
	if err := WriteMeta(repo, "arch-gridx", Meta{Inherits: "arch"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(Dir(repo, "arch-gridx"), metaFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"inherits\": \"arch\"\n}\n"; string(data) != want {
		t.Errorf("profile.json = %q; want %q", data, want)
	}
	m, err := ReadMeta(repo, "arch-gridx")
	if err != nil {
		t.Fatal(err)
	}
	if m.Inherits != "arch" {
		t.Errorf("Inherits = %q; want arch", m.Inherits)
	}

	if err := WriteMeta(repo, "arch-gridx", Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("profile.json still present after zero write: %v", err)
	}
	if !Exists(repo, "arch-gridx") {
		t.Error("profile dir removed along with meta")
	}
	// Removing twice is fine.
	if err := WriteMeta(repo, "arch-gridx", Meta{}); err != nil {
		t.Errorf("second zero write: %v", err)
	}
}

func TestWriteMeta_EmptyName(t *testing.T) {
	if err := WriteMeta(t.TempDir(), "", Meta{Inherits: "x"}); err == nil {
		t.Error("want error for empty name")
	}
}

func TestList(t *testing.T) {
	repo := t.TempDir()
	got, err := List(repo)
	if err != nil || got != nil {
		t.Fatalf("List(no profiles dir) = %v, %v; want nil, nil", got, err)
	}
	mkProfile(t, repo, "work", "")
	mkProfile(t, repo, "arch", "")
	if err := os.WriteFile(filepath.Join(repo, "profiles", "README"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = List(repo)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"arch", "work"}; !reflect.DeepEqual(got, want) {
		t.Errorf("List = %v; want %v", got, want)
	}
}

func TestStandalone(t *testing.T) {
	tests := []struct {
		name    string
		metas   map[string]Meta
		profile string
		want    bool
		wantErr bool
	}{
		{name: "empty name", profile: "", want: false},
		{name: "leaf without directory", profile: "ghost", want: false},
		{
			name:    "overlay",
			metas:   map[string]Meta{"arch": {}},
			profile: "arch",
			want:    false,
		},
		{
			name:    "leaf standalone",
			metas:   map[string]Meta{"server": {Standalone: true}},
			profile: "server",
			want:    true,
		},
		{
			name: "inherited from ancestor",
			metas: map[string]Meta{
				"server":    {Standalone: true},
				"server-db": {Inherits: "server"},
			},
			profile: "server-db",
			want:    true,
		},
		{
			name: "child standalone, parent not",
			metas: map[string]Meta{
				"arch":       {},
				"arch-gridx": {Inherits: "arch", Standalone: true},
			},
			profile: "arch-gridx",
			want:    true,
		},
		{
			name:    "broken chain",
			metas:   map[string]Meta{"broken": {Inherits: "ghost"}},
			profile: "broken",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := t.TempDir()
			for name, m := range tt.metas {
				mkProfile(t, repo, name, "")
				if err := WriteMeta(repo, name, m); err != nil {
					t.Fatal(err)
				}
			}
			got, err := Standalone(repo, tt.profile)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v; wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Standalone(%q) = %v; want %v", tt.profile, got, tt.want)
			}
		})
	}
}

func TestRootIncludes(t *testing.T) {
	tests := []struct {
		name       string
		metas      map[string]Meta
		profile    string
		standalone bool
		want       []string
		wantErr    bool
	}{
		{name: "empty name", profile: ""},
		{
			name:       "single layer",
			metas:      map[string]Meta{"server": {Standalone: true, Root: []string{"~/.zshrc"}}},
			profile:    "server",
			standalone: true,
			want:       []string{"~/.zshrc"},
		},
		{
			name: "union with duplicate, ancestor first",
			metas: map[string]Meta{
				"server":    {Standalone: true, Root: []string{"~/.zshrc", "~/.config/nvim"}},
				"server-db": {Inherits: "server", Root: []string{"~/.config/nvim", "~/.psqlrc"}},
			},
			profile:    "server-db",
			standalone: true,
			want:       []string{"~/.zshrc", "~/.config/nvim", "~/.psqlrc"},
		},
		{
			name: "child without root inherits parent list",
			metas: map[string]Meta{
				"server":    {Standalone: true, Root: []string{"~/.zshrc"}},
				"server-db": {Inherits: "server"},
			},
			profile:    "server-db",
			standalone: true,
			want:       []string{"~/.zshrc"},
		},
		{
			name:    "returned when not standalone",
			metas:   map[string]Meta{"arch": {Root: []string{"~/.zshrc"}}},
			profile: "arch",
			want:    []string{"~/.zshrc"},
		},
		{
			name:       "standalone without root",
			metas:      map[string]Meta{"server": {Standalone: true}},
			profile:    "server",
			standalone: true,
		},
		{
			name:    "broken chain",
			metas:   map[string]Meta{"broken": {Inherits: "ghost"}},
			profile: "broken",
			wantErr: true,
		},
		{
			name:    "empty entry",
			metas:   map[string]Meta{"server": {Standalone: true, Root: []string{""}}},
			profile: "server",
			wantErr: true,
		},
		{
			name:    "bare tilde entry",
			metas:   map[string]Meta{"server": {Standalone: true, Root: []string{"~"}}},
			profile: "server",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := t.TempDir()
			for name, m := range tt.metas {
				mkProfile(t, repo, name, "")
				if err := WriteMeta(repo, name, m); err != nil {
					t.Fatal(err)
				}
			}
			standalone, got, err := RootIncludes(repo, tt.profile)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v; wantErr %v", err, tt.wantErr)
			}
			if standalone != tt.standalone {
				t.Errorf("standalone = %v; want %v", standalone, tt.standalone)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("root = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestWriteMeta_RootRoundTrip(t *testing.T) {
	repo := t.TempDir()
	want := Meta{Standalone: true, Root: []string{"~/.zshrc", "~/.config/nvim"}}
	if err := WriteMeta(repo, "server", want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadMeta(repo, "server")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadMeta = %+v; want %+v", got, want)
	}
	if err := WriteMeta(repo, "rootonly", Meta{Root: []string{"~/.zshrc"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(Dir(repo, "rootonly"), metaFile)); err != nil {
		t.Errorf("Root-only meta must not count as zero: %v", err)
	}
}

func TestWriteMeta_FailedWriteKeepsPrevious(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	repo := t.TempDir()
	if err := WriteMeta(repo, "work", Meta{Inherits: "base"}); err != nil {
		t.Fatal(err)
	}
	dir := Dir(repo, "work")
	path := filepath.Join(dir, metaFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if err := WriteMeta(repo, "work", Meta{Inherits: "other"}); err == nil {
		t.Fatal("WriteMeta into read-only dir succeeded")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("profile.json changed by failed write: %q", after)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("leftover files: %v", entries)
	}
}
