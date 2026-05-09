# Deterministic Setup Feature — Implementation Plan

## manifest.toml schema

```toml
[packages]
brew   = ["ripgrep", "fd", "fzf", "starship"]
apt    = ["ripgrep", "fd-find", "fzf"]
pacman = ["ripgrep", "fd", "fzf", "starship"]

[dirs]
paths = [
  "~/tmp",
  "~/workspace/github",
]

[[repos]]
url  = "github.com/alexjoedt/dotfiles"
dest = "~/workspace/github/alexjoedt/dotfiles"

[[repos]]
url  = "github.com/alexjoedt/dman"
dest = "~/workspace/github/alexjoedt/dman"
```

## File location

`manifest.toml` lives in the root of the cloned dotfiles repo:

```
dotfiles/
  base/
  profiles/
  manifest.toml
```

Path resolved at runtime: `filepath.Join(cfg.Path, "manifest.toml")`

---

## Implementation Steps

### Step 1: Add TOML dependency

```
go get github.com/BurntSushi/toml
```

### Step 2: Add manifest.go

New file `manifest.go` in package `dman`. Contains:

```go
type Manifest struct {
    Packages Packages `toml:"packages"`
    Dirs     Dirs     `toml:"dirs"`
    Repos    []Repo   `toml:"repos"`
}

type Packages struct {
    Brew   []string `toml:"brew"`
    Apt    []string `toml:"apt"`
    Pacman []string `toml:"pacman"`
}

type Dirs struct {
    Paths []string `toml:"paths"`
}

type Repo struct {
    URL  string `toml:"url"`
    Dest string `toml:"dest"`
}
```

Helpers in the same file:
- `readManifest(path string) (*Manifest, error)` — decode TOML, return `nil, nil` if file absent (manifest is optional)
- `detectManager() string` — `LookPath` in order: `brew`, `yay`, `paru`, `pacman`, `apt-get`; returns first found or `""`
- `expandHome(path, home string) string` — replace leading `~/` with home dir

### Step 3: Implement `(a *App) Setup(ctx, dryRun)` in commands.go

Order of operations:
1. `readConfig()` → get `cfg.Path`
2. `readManifest(filepath.Join(cfg.Path, "manifest.toml"))` → if nil, return "no manifest.toml found"
3. **Install packages** — detect manager, resolve package list for that manager, run install command
   - brew: `brew install <packages...>`
   - apt-get: `sudo apt-get install -y <packages...>`
   - pacman/yay/paru: `sudo pacman -S --needed --noconfirm <packages...>` / `yay -S --needed --noconfirm <packages...>`
   - no manager found: print warning, skip
4. **Create dirs** — expand `~/`, `os.MkdirAll` each path; skip if already exists (idempotent)
5. **Clone repos** — expand `~/` on dest; skip if dest already exists; skip if git not installed; call `cloneRepo(ctx, url, dest)` (already in git.go)

Dry-run: print each action instead of executing.

### Step 4: Wire CLI command in cmd/main.go

```go
{
    Name:  "setup",
    Usage: "install packages, create dirs, and clone repos from manifest.toml",
    Flags: []cli.Flag{
        &cli.BoolFlag{Name: "dry-run", Usage: "show what would happen without making changes"},
    },
    Action: func(ctx context.Context, c *cli.Command) error {
        return app.Provision(ctx, c.Bool("dry-run"))
    },
},
```

### Step 5: Tests — manifest_test.go

- `TestReadManifest_RoundTrip` — write a temp `manifest.toml`, parse it, assert fields
- `TestReadManifest_Missing` — absent file returns `nil, nil`
- `TestExpandHome` — table-driven: `~/foo` → `/home/user/foo`, non-tilde path unchanged
- `TestDetectManager` — hard to unit test (LookPath); skip or integration-only

### Step 6: Verification

```
go build ./...
go vet ./...
go test ./... -count=1
```

---

## Notes

- `manifest.toml` is **optional** — `setup` is a no-op with a clear message if absent
- All steps are **idempotent**: safe to re-run on an already-provisioned machine
- Repo clone skips if dest dir already exists (non-empty check via `os.ReadDir`)
- Package install delegates idempotency to the manager (`--needed` for pacman, brew handles it natively)
- Tilde expansion must happen in Go — the shell does not expand `~/` in programmatic exec calls