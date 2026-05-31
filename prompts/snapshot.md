# Snapshot Feature — Refined Implementation Plan

**Source:** SCRATCH.md "Snapshots" section  
**Objective:** Before `dman apply` overwrites local dotfiles, persist a point-in-time snapshot of the affected files. Users can list, inspect, and restore snapshots.

---

## Issues Found in the Original Plan

### 1. Terminology conflict: "snapshot" vs "backup"
The codebase already has a `backupFile()` function and `App.BackupDir` (`~/.local/state/dman/backups`). The original plan uses both "snapshot" and `dman backup` as a command name, creating ambiguity between two distinct mechanisms.

**Fix:** Keep the existing per-file `.bak` backup as-is (it's lightweight and always-on). Name the new feature exclusively "snapshot". The manual create command should be `dman snapshot create`, not `dman backup`.

---

### 2. Missing restore command — critical gap
The plan says "restore snapshots" is a goal but never defines the command. Without restore, the feature has no recovery value.

**Fix:** Add `dman snapshot restore <snapshot-id>` as a required command.

---

### 3. Flat command names will pollute the top-level namespace
`dman list <snapshot-id>` occupies the `list` verb globally. If a future command wants to list tracked dotfiles (`dman list`) this will conflict. `dman cat <checksum>` is similarly flat and ambiguous.

**Fix:** Use a `snapshot` subcommand group:
```
dman snapshot list
dman snapshot create [--message <m>]
dman snapshot show <snapshot-id>
dman snapshot restore <snapshot-id>
dman snapshot cat <snapshot-id> <path>
dman snapshot delete <snapshot-id>
```

---

### 4. `dman cat <checksum>` is wrong — blobfs uses string keys, not content hashes
`blobfs` stores content under user-defined **keys** (e.g. `<snapshot-id>/.zshrc`). The SHA-256 is used internally for deduplication but is not part of the public API. There is no `Get(ctx, sha256hash)` call.

**Fix:** Change the interface to `dman snapshot cat <snapshot-id> <home-relative-path>`, e.g.:
```bash
dman snapshot cat 01jx... .zshrc
dman snapshot cat 01jx... .config/nvim/init.lua
```

---

### 5. Missing config schema
The plan says "configure to enable snapshots in the dman config json" but never defines the field name, type, or default.

**Fix:** Extend `Config` (in `config.go`) with:
```go
type SnapshotConfig struct {
    Enabled bool   `json:"enabled"`
    Path    string `json:"path,omitempty"` // default: ~/.local/share/dman/snapshots
}
```
```json
{
  "repositoryURL": "...",
  "profile": "default",
  "path": "...",
  "snapshots": {
    "enabled": true
  }
}
```
Snapshots are **disabled by default**. When not set, the `snapshots` key can be omitted entirely.

---

### 6. Snapshot storage path conflicts with existing repo path
`~/.local/share/dman-snapshots` sits alongside `~/.local/share/dman` (the cloned dotfile repo). This is fine but means a second top-level directory. A cleaner alternative is `~/.local/share/dman/snapshots`, keeping everything under one root.

**Recommendation:** Default to `~/.local/share/dman/snapshots`. The `Path` override in `SnapshotConfig` allows users to change it. Note: `~/.local/share/dman` is already created by `Init`; `NewApp` or `SnapshotStore` initialization should create the `snapshots/` subdirectory only when snapshots are enabled.

---

### 7. UUID v7 dependency missing from go.mod
`github.com/google/uuid` is not in `go.mod`. UUID v7 (time-ordered) is supported from `v1.5.0+`.

**Fix:** `go get github.com/google/uuid@latest`

---

### 8. Index design not specified
The plan says "create an index" without defining its schema or storage location.

**Fix:** Store a single `index.json` file at the root of the snapshot directory (not inside blobfs). Format:
```go
type SnapshotIndex struct {
    Snapshots []SnapshotMeta `json:"snapshots"`
}

type SnapshotMeta struct {
    ID        string    `json:"id"`        // UUID v7
    CreatedAt time.Time `json:"createdAt"`
    Message   string    `json:"message,omitempty"` // from --message flag
    FileCount int       `json:"fileCount"`
}
```
The index is append-only. Deletion removes an entry and triggers `storage.GC(ctx)` to reclaim orphaned blobs.

---

### 9. Per-snapshot file manifest not defined
To support `dman snapshot show` and `dman snapshot cat`, each snapshot needs a list of which files it contains and their blobfs keys.

**Fix:** Store one `<snapshot-id>.json` manifest per snapshot alongside the index:
```go
type SnapshotManifest struct {
    ID    string         `json:"id"`
    Files []SnapshotFile `json:"files"`
}

type SnapshotFile struct {
    Path string `json:"path"` // home-relative, e.g. ".zshrc"
    Key  string `json:"key"`  // blobfs key: "<snapshot-id>/<path>"
    Size int64  `json:"size"`
}
```

---

### 10. Snapshot scope during `Apply` is ambiguous
Should the auto-snapshot capture all tracked dotfiles (full snapshot) or only files that are about to be overwritten?

**Recommendation:** Capture all **currently tracked dotfiles** (full snapshot) before any writes. This is predictable and recoverable without needing to compute the diff first. It also means `restore` is straightforward — just write every file in the manifest.

---

### 11. Auto-snapshot failure handling in `Apply` not defined
If `SnapshotCreate` fails during `Apply`, should the apply abort or warn and continue?

**Fix:** Auto-snapshot failure should **abort the apply** with a clear error. The purpose of snapshots is to provide a safety net before destructive writes; silently skipping it defeats the purpose. If the user wants to skip, they can use `--no-snapshot` flag or disable in config.

---

### 12. No GC strategy defined
After `dman snapshot delete <id>`, orphaned blobs in blobfs must be reclaimed. blobfs provides `storage.GC(ctx)`.

**Fix:** Call `storage.GC(ctx)` after every delete operation. For `restore`, no GC is needed.

---

## Refined Implementation Plan

### Phase 1 — Dependencies and Config

**1.1** Add dependencies:
```bash
go get github.com/google/uuid@latest
go get github.com/alexjoedt/blobfs@latest
```

**1.2** Extend `Config` in `config.go`:
- Add `Snapshots *SnapshotConfig` field (pointer so omitempty works correctly)
- `SnapshotConfig` has `Enabled bool` and optional `Path string`

**1.3** Add `SnapshotDir string` to `App` struct in `dman.go`.

**1.4** In `NewApp()`, initialize `SnapshotDir` only when config exists and `snapshots.enabled` is true. Leave `SnapshotDir` empty otherwise. Directory creation happens lazily in `SnapshotStore`.

---

### Phase 2 — Snapshot Storage Layer

Create `snapshot.go` (new file) containing:

**2.1** `SnapshotStore` struct:
```go
type SnapshotStore struct {
    dir     string           // root snapshot directory
    storage *blobfs.Storage  // blobfs instance
}
```

**2.2** `newSnapshotStore(dir string) (*SnapshotStore, error)`:
- Creates `dir` if not exists
- Initializes `blobfs.NewStorage(filepath.Join(dir, "blobs"), blobfs.WithCompression(blobfs.CodecZstd), blobfs.WithVerifyOnRead(true))`
- Returns the store

**2.3** Index operations (JSON marshal/unmarshal `index.json`):
- `(s *SnapshotStore) loadIndex() (*SnapshotIndex, error)`
- `(s *SnapshotStore) saveIndex(idx *SnapshotIndex) error`

**2.4** Manifest operations:
- `(s *SnapshotStore) loadManifest(id string) (*SnapshotManifest, error)`
- `(s *SnapshotStore) saveManifest(m *SnapshotManifest) error`

**2.5** `(s *SnapshotStore) Create(ctx, homeDir, files []string, message string) (SnapshotMeta, error)`:
- Generates UUID v7 as snapshot ID (`uuid.NewV7()`)
- For each file path, compute blobfs key as `<snapshotID>/<home-relative-path>`
- Calls `storage.Put(ctx, key, fileReader)` for each file
- Builds `SnapshotManifest` and saves it
- Appends `SnapshotMeta` to index and saves it
- Returns `SnapshotMeta`

**2.6** `(s *SnapshotStore) List() ([]SnapshotMeta, error)` — reads index

**2.7** `(s *SnapshotStore) Files(id string) ([]SnapshotFile, error)` — reads manifest

**2.8** `(s *SnapshotStore) Cat(ctx, id, path string) (io.ReadCloser, error)`:
- Computes key from id + path
- Returns `storage.Get(ctx, key)`

**2.9** `(s *SnapshotStore) Restore(ctx, id, homeDir string) error`:
- Loads manifest
- For each file, calls `storage.Get(ctx, key)` and writes to `<homeDir>/<path>` using `copyFile` equivalent
- Preserves file permissions from the manifest (add `Mode fs.FileMode` to `SnapshotFile`)

**2.10** `(s *SnapshotStore) Delete(ctx, id string) error`:
- Loads manifest, deletes each blob via `storage.Delete(ctx, key)`
- Removes manifest file
- Removes entry from index, saves index
- Calls `storage.GC(ctx)`

---

### Phase 3 — App Methods

Add to `commands.go` (or a new `snapshot_commands.go`):

**3.1** Helper `(a *App) snapshotStore(cfg *Config) (*SnapshotStore, error)`:
- Returns error if `cfg.Snapshots == nil || !cfg.Snapshots.Enabled`
- Resolves path: `cfg.Snapshots.Path` or `filepath.Join(a.HomeDir, ".local", "share", "dman", "snapshots")`
- Returns `newSnapshotStore(path)`

**3.2** `(a *App) SnapshotCreate(ctx, message string) error`

**3.3** `(a *App) SnapshotList(ctx) error` — prints table: ID | Date | Files | Message

**3.4** `(a *App) SnapshotShow(ctx, id string) error` — prints file list for snapshot

**3.5** `(a *App) SnapshotRestore(ctx, id string) error` — restores all files, prints count

**3.6** `(a *App) SnapshotCat(ctx, id, path string) error` — streams blob to stdout

**3.7** `(a *App) SnapshotDelete(ctx, id string) error`

**3.8** Modify `Apply()`:
- After collecting `pairs` (merged, deduped), if snapshots enabled:
  ```go
  if cfg.Snapshots != nil && cfg.Snapshots.Enabled && !dryRun {
      if err := a.autoSnapshot(ctx, cfg, pairs); err != nil {
          return fmt.Errorf("snapshot before apply: %w", err)
      }
  }
  ```
- `autoSnapshot` collects the destination paths of all `pairs` that exist on disk and calls `SnapshotCreate`.

---

### Phase 4 — CLI Commands

Add to `cmd/main.go`, as a new top-level `snapshot` command with subcommands:

```go
{
    Name:  "snapshot",
    Usage: "manage dotfile snapshots",
    Commands: []*cli.Command{
        {Name: "list",    ...},
        {Name: "create",  Flags: [--message/-m string], ...},
        {Name: "show",    ArgsUsage: "<snapshot-id>", ...},
        {Name: "restore", ArgsUsage: "<snapshot-id>", ...},
        {Name: "cat",     ArgsUsage: "<snapshot-id> <path>", ...},
        {Name: "delete",  ArgsUsage: "<snapshot-id>", ...},
    },
}
```

---

### Phase 5 — Tests

- `snapshot_test.go`: test `SnapshotStore` with a temp directory
  - Create → List (count=1) → Show (correct files) → Cat (correct content) → Delete → List (count=0)
  - Deduplication: two snapshots with identical file content → single blob on disk (verify via `GC` stats returning 0)
  - Restore: create snapshot, modify file, restore, verify content matches original
- Modify `TestApply` (if it exists) to cover auto-snapshot path

---

## Acceptance Criteria

- `dman snapshot create` stores a full snapshot of all currently tracked dotfiles; output includes the snapshot ID
- `dman snapshot list` prints a table with ID, timestamp, file count, and message
- `dman snapshot show <id>` prints home-relative paths of all files in the snapshot
- `dman snapshot restore <id>` overwrites home files with snapshot contents and prints the count restored
- `dman snapshot cat <id> <path>` prints file content to stdout; works with pipes (`dman snapshot cat <id> .zshrc | diff - ~/.zshrc`)
- `dman snapshot delete <id>` removes the snapshot and reclaims storage
- When `snapshots.enabled: true` in config, `dman apply` creates a snapshot before writing any file; apply aborts if snapshot creation fails
- When `snapshots.enabled: false` (or absent), all snapshot commands print a clear "snapshots not enabled" error
- Identical file content across snapshots is stored only once on disk (blobfs deduplication)
- All new code passes `go vet` and existing tests remain green
