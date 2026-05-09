# Plan: Content-Addressable Object Store

**Status**: draft
**Date**: 2026-03-09

---

## Overview

Currently, `Dotfile.Data []byte` stores raw file content inside BoltDB. Every snapshot embeds
full file content as base64-encoded JSON in a BoltDB page file. This means:

- The DB grows unboundedly with each snapshot, even when dotfiles haven't changed.
- Every read of a snapshot loads all file bytes into memory.
- Deduplication across snapshots is impossible.

This plan decouples content from metadata. BoltDB keeps only tiny metadata records. Raw file
content lives in a **content-addressable object store** backed by
[`alexjoedt/blobfs`](https://github.com/alexjoedt/blobfs).

`blobfs` is natively CAS — its package doc states "content-addressable blob storage system
that stores files using SHA-256 hashing for deduplication and integrity." Internally it
maintains a `refs/` index (sharded by `SHA-256(key)`) and an `objects/` store (sharded by
`SHA-256(content)`). Two different keys with identical content share a single `objects/` entry.
Using the file's SHA-256 hex as the key is the canonical usage pattern, matching the `NewBlob`
example in the library docs.

---

## Design

### Storage Layout

```
~/.config/dman/
    dman.db          ← BoltDB: snapshot/dotfile metadata only (no content)
    objects/         ← blobfs root (passed to NewStorage)
        refs/        ← sharded by SHA-256(key); each shard holds data + meta.json
        objects/     ← CAS store sharded by SHA-256(content); deduplicated blobs
        .tmp/        ← staging area for atomic writes
```

### `Dotfile` struct change

```go
// Before
type Dotfile struct {
    ID        []byte   `json:"id"`
    CreatedAt DateTime `json:"date_created"`
    Name      string   `json:"name"`
    Data      []byte   `json:"data"`    // ← removed
}

// After
type Dotfile struct {
    ID        []byte   `json:"id"`
    CreatedAt DateTime `json:"date_created"`
    Name      string   `json:"name"`
    Hash      string   `json:"hash"`    // ← SHA-256 hex = blobfs key
}
```

### `App` struct change

```go
type App struct {
    HomeDir   string
    HomeMode  os.FileMode
    RepoDir   string
    ConfigDir string
    DBPath    string
    Blobs     *blobfs.Storage  // ← new
}
```

---

## Implementation Steps

### Step 1: Add dependency

```bash
go get github.com/alexjoedt/blobfs
```

### Step 2: Init blobfs in `NewApp()`

In `dman.go`, after resolving `ConfigDir`:

```go
blobsDir := filepath.Join(a.ConfigDir, "objects")
a.Blobs, err = blobfs.NewStorage(blobsDir,
    blobfs.WithFileMode(info.Mode()),
    blobfs.WithDirMode(info.Mode()),
)
if err != nil {
    return nil, fmt.Errorf("init object store: %w", err)
}
```

### Step 3: Update `Dotfile` struct in `snapshot.go`

- Remove `Data []byte` field.
- Add `Hash string` field.
- Remove `os.ReadFile` call from `NewDotfile()` — it no longer reads content.

```go
func NewDotfile(hash string, path string) *Dotfile {
    return &Dotfile{
        ID:   []byte(hash),
        Hash: hash,
        Name: path,
    }
}
```

### Step 4: Update `createSnapshot` in `db.go`

**Ordering (atomicity)**: write the blob first, then commit the DB transaction. If the DB
commit fails the orphaned blob is harmless — GC will remove it. If the blob write fails,
the DB transaction never starts, so no dangling reference is created.

The canonical blobfs CAS pattern (from the `NewBlob` docs) reads the file only once and
obtains the hash from the blob itself — avoiding the `getHash` + re-open double-read:

```go
blob, err := blobs.NewBlob()
if err != nil {
    return fmt.Errorf("new blob: %w", err)
}
defer blob.Discard() //nolint:errcheck

f, err := os.Open(filePath)
if err != nil {
    return fmt.Errorf("open dotfile: %w", err)
}
defer f.Close()

if _, err := io.Copy(blob, f); err != nil {
    return fmt.Errorf("stream dotfile to blob: %w", err)
}

hash := blob.Hash() // SHA-256 hex computed during write — no second file read

exists, err := blobs.Exists(ctx, hash)
if err != nil {
    return fmt.Errorf("check blob existence: %w", err)
}
if !exists {
    if err := blob.CommitAs(hash); err != nil {
        return fmt.Errorf("commit blob: %w", err)
    }
}
// blob is now either committed or skipped; proceed to DB write
```

Because `getHash(f)` is replaced by `blob.Hash()`, the file is read exactly once.
The hash is used as both the `Dotfile.Hash` field and the blobfs key.

`createSnapshot` must be lifted out of its current form — file I/O (including hash
computation) must not happen inside a BoltDB `db.Update` callback. The refactored flow:

1. For each file path: stream into `NewBlob`, collect `(path, hash)` pairs, commit blobs.
2. Open `db.Update` transaction using the collected pairs — no file I/O inside the tx.

Updated signature (move `ctx` to first position per Go convention):

```go
func createSnapshot(ctx context.Context, db *bolt.DB, blobs *blobfs.Storage, files []string, tags ...string) error
```

**Update all three call sites** in `commands.go`:

| Function  | Old call                                   | New call                                                   |
|-----------|--------------------------------------------|------------------------------------------------------------|
| `Apply`   | `createSnapshot(db, homePaths(pairs), ...)` | `createSnapshot(ctx, db, a.Blobs, homePaths(pairs), ...)` |
| `Restore` | `createSnapshot(db, homePaths(pairs), ...)` | `createSnapshot(ctx, db, a.Blobs, homePaths(pairs), ...)` |
| `Backup`  | `createSnapshot(db, homePaths(pairs), ...)` | `createSnapshot(ctx, db, a.Blobs, homePaths(pairs), ...)` |

> **Note**: `Backup` is a third call site omitted from the original plan's Files Changed
> table. Missing it will produce a compile error.

### Step 5: Update `Cat` in `commands.go`

```go
// Before
fmt.Fprintln(os.Stdout, string(dotfile.Data))

// After
r, err := a.Blobs.Get(ctx, dotfile.Hash)
if err != nil {
    return fmt.Errorf("get blob: %w", err)
}
defer r.Close()
_, err = io.Copy(os.Stdout, r)
return err
```

`Cat` already receives `ctx context.Context` — no signature change needed.

### Step 6: Update `restoreDotfile` in `commands.go`

Add `ctx context.Context` parameter (needed for `a.Blobs.Get`). Update the caller `Restore`
to pass `ctx`. Replace the `os.WriteFile` call:

```go
// Before
func (a *App) restoreDotfile(dotfile *Dotfile) error {
    ...
    os.WriteFile(dotfile.Name, dotfile.Data, 0o644)

// After
func (a *App) restoreDotfile(ctx context.Context, dotfile *Dotfile) error {
    dir := filepath.Dir(dotfile.Name)
    if err := os.MkdirAll(dir, a.HomeMode); err != nil {
        return fmt.Errorf("create directory %s: %w", dir, err)
    }

    r, err := a.Blobs.Get(ctx, dotfile.Hash)
    if err != nil {
        return fmt.Errorf("get blob for restore: %w", err)
    }
    defer r.Close()

    f, err := os.OpenFile(dotfile.Name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, a.HomeMode.Perm())
    if err != nil {
        return fmt.Errorf("open file for restore: %w", err)
    }
    defer f.Close()

    _, err = io.Copy(f, r)
    return err
}
```

> `a.HomeMode.Perm()` (not `a.HomeMode`) strips the type bits — `os.FileMode` from
> `os.Stat` includes directory type bits that must not be passed to `os.OpenFile`.

---

## Migration

Existing users have `Dotfile.Data` populated and no `objects/` directory.

### Detection

Perform the migration check inside `openDB()` (or a new `checkMigrationNeeded` helper called
at the top of every command that opens the DB). The check: open a read-only view and scan
the `dotfiles` bucket for any record with a non-empty `Data` field.

Do not silently proceed. Return a typed sentinel:

```go
var ErrMigrationRequired = errors.New(
    `database was created with an older version of dman.
Run "dman migrate" to move dotfile content to the object store.`)
```

Every command that calls `openDB()` will surface this error before doing any work.

> **Do not** put this check in `NewApp()` — `NewApp` doesn't open the DB and adding DB
> I/O there would make startup slower and harder to test.

### `dman migrate` command

Migration must be resumable (idempotent): if interrupted, re-running must pick up where it
left off. Use one `db.Update` per dotfile so each write is independently committed:

1. Open BoltDB.
2. Walk all dotfiles via `listAllDotfiles`.
3. For each dotfile where `Hash == ""` and `len(Data) > 0`:
   a. Compute SHA-256 of `Data`.
   b. `blobs.Put(ctx, hash, bytes.NewReader(data))` — idempotent if blob already exists.
   c. In a separate `db.Update`: set `Hash = hex`, zero `Data = nil`.
4. Print `Migrated N dotfiles`.

> Step (c) must be a separate transaction from the blob write so that if the process is
> killed between blob write and DB update, re-running detects `Hash == ""` and repeats
> safely (blob write is idempotent via `Put`).

---

## GC Command

Once blobs are decoupled from snapshots, a blob can become orphaned if a snapshot is deleted
(future `dman purge` work). Add a `dman gc` command:

1. Collect all `Dotfile.Hash` values from BoltDB → `referenced` map.
2. Walk all blobs: `a.Blobs.Walk(ctx, "", func(key string, meta *blobfs.Meta, err error) error {...})`.
   - `Walk` is synchronous and needs no cleanup (unlike the deprecated `List`/`BlobResult`).
3. For each `key` not in `referenced`, call `a.Blobs.Delete(ctx, key)`.
4. Print `Removed N unreferenced blobs`.

---

## Key Considerations

| Topic | Decision |
|---|---|
| Blob key | SHA-256 hex string (same as `getHash()` output; use `blob.Hash()` from `NewBlob` to read file only once) |
| Blob dir | `~/.config/dman/objects/` — co-located with DB for simplicity |
| Sharding | `blobfs.DefaultShardFunc` (two-level, e.g. `refs/ab/cd/...`) |
| File permissions | `a.HomeMode.Perm()` — `.Perm()` strips type bits; `.Mode()` alone is incorrect for `os.OpenFile` |
| Deduplication | blobfs native CAS; `Exists(ctx, hash)` returns `(bool, error)` — handle the error |
| CGo | `blobfs` is pure Go — no CGo, `go install` keeps working |
| Streaming | `Get` returns `io.ReadCloser` — large files never fully loaded into memory |
| Read verification | Consider `blobfs.WithVerifyOnRead(true)` for `dman cat`/restore — detects silent corruption |
| Blob list API | Use `Walk` (synchronous, no cleanup); `List`/`BlobResult` are deprecated in blobfs |

---

## Risk

- **Migration**: existing DB files are broken until `dman migrate` runs. Must be surfaced
  clearly on first run after upgrade, not silently corrupt. Implemented via `ErrMigrationRequired`
  returned from a per-command check before any DB work.
- **Atomicity**: write blob first, then commit DB transaction. If the DB commit fails, the
  orphaned blob is cleaned up by `dman gc`. If `blob.CommitAs` fails, no DB record is written —
  no dangling reference is created. **Do not** write to BoltDB first (contradiction in original
  plan's Step 4).
- **File I/O in BoltDB transactions**: the current `getHash(f)` call is inside `db.Update`.
  With the refactored `NewBlob` approach, all file I/O happens before the transaction opens —
  eliminating this antipattern.
- **blobfs maturity**: the library is personal/pre-production. Since dman is also personal,
  this is acceptable — but pin to a specific release tag in `go.mod`.

---

## Files Changed

| File | Change |
|---|---|
| `go.mod` | add `github.com/alexjoedt/blobfs` |
| `dman.go` | add `Blobs *blobfs.Storage` to `App`; init in `NewApp()` |
| `snapshot.go` | remove `Data []byte`, add `Hash string`; simplify `NewDotfile` |
| `db.go` | `createSnapshot`: add `ctx`, `blobs`; move file I/O before tx; write blob before DB commit |
| `commands.go` | `Cat`, `restoreDotfile`: stream from `Blobs.Get()` |
| `commands.go` | `Apply`, `Restore`, **`Backup`**: pass `ctx` and `a.Blobs` to `createSnapshot` |
| `commands.go` | add migration guard (call `checkMigrationNeeded` at top of DB-using commands) |
| `cmd/main.go` | add `migrate` and `gc` subcommands (register with `urfave/cli/v3`) |

---

## Next Steps

1. `go get github.com/alexjoedt/blobfs@latest` and pin to a release tag
2. Implement steps 2–6 above (core change, ~2h)
3. Implement `dman migrate` (~30 min)
4. Implement `dman gc` (~30 min)
5. Update tests: `db_test.go` needs a `blobfs.Storage` backed by `t.TempDir()` (blobfs has
   no in-memory mode — `t.TempDir()` is the correct approach)
6. Manual test: `dman backup`, `dman cat <id>`, `dman restore <snapshot>`
