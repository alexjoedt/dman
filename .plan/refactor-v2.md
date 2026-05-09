# dman Refactor Plan — Overlay Architecture

## Objective

Replace the CAS/BoltDB + Git-branch architecture with a simple directory-based
overlay architecture (base + profile overrides) and flat-file backups.
CLI framework: `github.com/urfave/cli/v3`.

---

## Target Repository Layout (managed dotfile repo)

```
.
├── base/                         # Base dotfiles for all systems (dot_ prefix)
│   ├── dot_zshrc
│   └── scripts/                  # Base scripts, run on every apply
│       └── 01_setup.sh
├── profiles/                     # Environment-specific overrides and scripts
│   ├── work/
│   │   ├── dot_zshrc             # Overrides / additions to base/ (flat, no dotfiles/ subdir)
│   │   └── scripts/              # Profile scripts, run after base scripts
│   │       └── 01_install.sh
│   └── private/
│       ├── dot_zshrc
│       └── scripts/
└── dman.toml                     # Declarative repo config (reserved for future use)
```

**Convention:** Inside a profile directory, entries are classified as follows:
- `scripts/` — reserved directory name; contains executable scripts.
- Any other lowercase directory name — reserved for future use.
- Files or directories starting with `dot_` — dotfiles; walked recursively for copying.

**Naming convention:** `dot_` prefix replaces the leading `.` in filenames.
Only the **first** path segment gets the prefix (e.g. `~/.config/nvim/init.lua`
→ `base/dot_config/nvim/init.lua`).

---

## Config & State

### Operational State — `~/.config/dman/dman.json`

```go
// Config is the operational state stored at ~/.config/dman/dman.json.
type Config struct {
    RepositoryURL string `json:"repositoryURL"` // remote git repository URL
    Profile       string `json:"profile"`        // active profile name
    Path          string `json:"path"`            // absolute path to local repo clone
}
```

- `RepositoryURL`: remote URL provided to `dman init`.
- `Profile`: name of the currently active profile (default: `"default"`).
- `Path`: absolute path of the local clone (default: `~/.local/share/dman`).
- Saved/loaded with `json.Encoder` / `json.Decoder`; indented for readability.

### Repo Config — `<repo>/dman.toml`

Reserved for future declarative configuration. Create as an empty file during
`dman init`; ignore if absent.

---

## New `App` Struct (`dman.go`)

```go
type App struct {
    HomeDir   string
    HomeMode  os.FileMode
    ConfigDir string      // ~/.config/dman
    BackupDir string      // ~/.local/state/dman/backups
    // RepoDir and active profile are loaded from Config at command time,
    // not stored on App, to keep the zero value safe.
}
```

`NewApp()` responsibilities:
1. Resolve `HomeDir` via `os.UserHomeDir()`.
2. Create `~/.config/dman/` if absent.
3. Create `~/.local/state/dman/backups/` if absent.
4. **No** BoltDB / blobfs initialization.

---

## Phase 0 — Delete Old Code

### Files to delete entirely
| File | Reason |
|------|--------|
| `snapshot.go` | `Snapshot`, `Dotfile`, `DateTime` types; all snapshot logic |
| `db.go` | BoltDB wrappers, migration helpers |
| `db_test.go` | Tests for deleted types |

### Code blocks to remove from `commands.go`
- `Backup()`, `Cat()`, `Restore()`, `ListSnapshots()`, `Env()`
- Any import of `go.etcd.io/bbolt` or `github.com/alexjoedt/blobfs`

### Code blocks to remove from `cmd/main.go`
- CLI commands: `backup`, `snapshots`, `restore`, `list`, `cat`, `env`, `migrate`
- `--branch` flag on `init`

### `go.mod` — remove dependencies
```
go.etcd.io/bbolt
github.com/alexjoedt/blobfs
```
Run `go mod tidy` after deletion.

### Files to keep / reuse
| File | Action |
|------|--------|
| `git.go` | Keep; remove `ListBranches`, `CurrentBranch`, branch-switch logic |
| `hash.go` | Keep; used by `applyFiles` to skip unchanged files |
| `config.go` | Rewrite struct and filename |
| `dman.go` | Rewrite `App`, `NewApp`, keep `copyFile`, `isExist`, `transformPath` |
| `commands.go` | Remove deprecated methods; rewrite `Add`, `Apply`, `Init` |

---

## Phase 1 — Config Rewrite (`config.go`)

```go
var ErrNoConfig = errors.New("no dman config found, run: dman init <repo-url>")

type Config struct {
    RepositoryURL string `json:"repositoryURL"`
    Profile       string `json:"profile"`
    Path          string `json:"path"`
}

const configFileName = "dman.json"
```

- `saveConfig` / `readConfig` operate on `~/.config/dman/dman.json`.
- `readConfig` returns `ErrNoConfig` when the file is absent (sentinel, not
  wrapped) so callers can use `errors.Is`.

---

## Phase 2 — `dman init <repo-url>` 

**Flags:** `--destination <path>` (optional, default: `~/.local/share/dman`)

**Steps:**
1. Validate `<repo-url>` is non-empty; optionally parse as a URL.
2. If destination already exists → return error:
   `"destination already exists: <path>; remove it or use --destination"`.
3. `git clone <repo-url> <destination>`.
4. Validate cloned repo contains a `base/` directory; if not:
   return `"repository missing required base/ directory"`.
5. Write `~/.config/dman/dman.json`:
   ```json
   {
     "repositoryURL": "<url>",
     "profile": "default",
     "path": "<destination>"
   }
   ```
6. Print: `Initialized dman. Repository: <url>. Active profile: default`.

**Acceptance criteria:** After `init`, `dman apply` succeeds without additional
setup.

---

## Phase 3 — `dman apply [--profile <name>] [--dry-run] [--run-scripts]`

**Steps:**

```
1. readConfig() → cfg
2. profile = flag(--profile) ?? cfg.Profile
3. git pull in cfg.Path  (keep existing pull behavior)
4. Collect base pairs:
     walk cfg.Path/base/ recursively
     src = base/dot_foo/bar  →  dst = ~/dot_foo/bar with dot_ → . on first segment
5. Collect profile pairs (if profiles/<profile>/ exists):
     walk profiles/<profile>/ recursively
     skip the scripts/ subdirectory and any entry not starting with dot_
     same dot_ → . transformation
     profile pairs WIN over base (append after base; last wins in apply loop)
6. For each changed pair (srcHash != dstHash):
     a. if dst exists → backup(dst, cfg.BackupDir)
     b. os.MkdirAll(filepath.Dir(dst), app.HomeMode)
     c. copyFile(dst, src)            // preserves file mode from src
     d. print: "<src> --> <dst>"
7. If --run-scripts is set:
     a. base scripts: ReadDir(cfg.Path/base/scripts/), sort lexicographically,
        run each executable file via exec.CommandContext; stop on non-zero exit
     b. profile scripts: ReadDir(cfg.Path/profiles/<profile>/scripts/), same logic
        (skipped if directory does not exist)
8. Print summary: "Applied N file(s). Ran M script(s)." (base + profile scripts counted together)
```

**Dry-run:** print what would change; skip backup, copy, and scripts.

**Missing profile directory:** if `profiles/<profile>/` does not exist, log
`"Notice: no profile directory found for '<profile>', applying base only."` and
continue (base dotfiles and base scripts still run normally).

**Script execution:**
- Only files with the executable bit set are run.
- Scripts run in order: `01_install.sh`, `02_configure.sh`, …
- On non-zero exit code, execution halts and the error is returned.
- `--run-scripts` is `false` by default (opt-in for safety).

### Backup filename format

To avoid collisions with nested dotfiles, the backup name encodes the relative
home path:

```
~/.zshrc                    → _zshrc_20260509_120000.bak
~/.config/nvim/init.lua     → _config_nvim_init.lua_20260509_120000.bak
```

Algorithm: strip `~/`, replace `/` with `_`, replace leading `.` with `_`.

---

## Phase 4 — `dman add [--profile <name>] <file> [<file>...]`

**Steps:**

```
1. readConfig() → cfg
2. profile = flag(--profile) ?? cfg.Profile
3. For each file:
   a. Resolve absolute path; error if not under HomeDir
   b. Error if filename doesn't start with '.' ("not a dotfile: <file>")
   c. Compute dot_-encoded relative path via transformPath()
   d. Determine destination in repo:
        if --profile is explicitly set:
            dst = cfg.Path/profiles/<profile>/<dot_file>
        else if profile is set AND file exists at profiles/<profile>/<dot_file>:
            dst = cfg.Path/profiles/<profile>/<dot_file>  // update existing
        else:
            dst = cfg.Path/base/<dot_file>                // add to base
   e. os.MkdirAll(filepath.Dir(dst), 0755)
   f. copyFile(dst, src)
   g. record "add" or "update" in report
4. git add <changed files>
5. git commit -m "add/update <files>"
6. git push
```

**Nested dotfiles:** `~/.config/nvim/init.lua` → `base/dot_config/nvim/init.lua`
(only the first segment gets the `dot_` prefix).

**Non-dotfiles:** return `"not a dotfile: <file>"` (current behavior, keep it).

---

## Phase 5 — Retained & Removed Commands

| Command | Status | Notes |
|---------|--------|-------|
| `init` | **Rewrite** | Remove `--branch`; add `--destination` |
| `apply` | **Rewrite** | Profile overlay; flat-file backup |
| `add` | **Rewrite** | Profile-aware destination |
| `pull` | **Keep** | Thin wrapper around `git pull` |
| `push` | **Keep** | Thin wrapper around `git push` (optional if `add` auto-pushes) |
| `backup` | **Remove** | Replaced by per-file backup in `apply` |
| `restore` | **Remove** | Out of scope |
| `snapshots` / `list` | **Remove** | Out of scope |
| `cat` | **Remove** | Out of scope |
| `env` | **Remove** | Replaced by `--profile` / `profile` in config |
| `migrate` | **Remove** | No migration needed |

---

## Phase 6 — `copyFile` — Preserve File Permissions

Current `copyFile` uses `os.Create` (fixed mode `0666` before umask). Fix:

```go
func copyFile(dst, src string) error {
    srcInfo, err := os.Stat(src)
    if err != nil {
        return err
    }
    srcFile, err := os.Open(src)
    if err != nil {
        return err
    }
    defer srcFile.Close()

    if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
        return err
    }
    dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
    if err != nil {
        return err
    }
    defer dstFile.Close()

    _, err = io.Copy(dstFile, srcFile)
    return err
}
```

---

## Phase 7 — Tests

| File | Action |
|------|--------|
| `db_test.go` | **Delete** |
| `dman_test.go` | Rewrite: test `transformPath`, `applyFiles` merge behavior, backup name generation |
| `config_test.go` | New: round-trip `saveConfig` / `readConfig` |

**Table-driven tests required for:**
- `transformPath`: simple dotfile, nested dotfile, non-dotfile (expect error),
  path already relative
- Backup name generation: simple, nested, two files with same basename at
  different depths
- `applyFiles`: profile entry overrides base entry for same destination

---

## Execution Order

| Step | Action | Success Criteria |
|------|--------|-----------------|
| 1 | Delete `snapshot.go`, `db.go`, `db_test.go` | `go build ./...` fails only on remaining references |
| 2 | Remove deprecated methods from `commands.go` | No reference to `bbolt` or `blobfs` remains |
| 3 | Remove deprecated CLI commands from `cmd/main.go` | Compiles cleanly after step 4 |
| 4 | `go mod tidy` | `go.mod` no longer lists `bbolt` / `blobfs` |
| 5 | Rewrite `config.go` (new struct, new filename) | `readConfig` / `saveConfig` unit tests pass |
| 6 | Rewrite `dman.go` (`App`, `NewApp`, `copyFile`) | `go build ./...` succeeds |
| 7 | Implement `Init` | `dman init <url>` clones repo, writes `dman.json` |
| 8 | Implement `Apply` (base + profile + backup + scripts) | `dman apply` copies files, creates backups |
| 9 | Implement `Add` (profile-aware) | `dman add ~/.zshrc` lands in correct location |
| 10 | Update `cmd/main.go` | All new flags wired; removed commands gone |
| 11 | Write / update tests | `go test ./... -count=1` passes |
| 12 | Final build & lint | `go build ./...` and `go vet ./...` clean |

---

## Risk Register

| Risk | Severity | Mitigation |
|------|----------|------------|
| Script execution without confirmation | **High** | `--run-scripts` flag; off by default |
| Backup filename collision (same base name, different dirs) | Medium | Encode full relative path in backup name |
| `profiles/<profile>/` absent → silent skip | Low | Print visible notice before continuing |
| File permission loss in `copyFile` | Low | Fix `copyFile` to stat src and apply mode |
| Config file rename breaks existing installs | Low | Document; no migration in scope |
