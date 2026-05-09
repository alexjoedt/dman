# Execution: dman Refactor v2 — Overlay Architecture
**Source**: .plan/refactor-v2.md
**Status**: done
**Started**: 2026-05-09T00:00:00Z
**Updated**: 2026-05-09T12:00:00Z

## Progress

### Phase 0: Delete Old Code - done
- [x] Step 0.1: Delete snapshot.go, db.go, db_test.go
- [x] Step 0.2: Remove deprecated methods from commands.go (Backup, Cat, Restore, List, Snapshots, Env*, Migrate, GC)
- [x] Step 0.3: Remove deprecated CLI commands from cmd/main.go
- [x] Step 0.4: go mod tidy (removed bbolt, blobfs)

### Phase 1: Config Rewrite - done
- [x] Step 1.1: Rewrite config.go (new Config struct, dman.json, ErrNoConfig sentinel)

### Phase 2: dman.go Rewrite - done
- [x] Step 2.1: Rewrite App struct and NewApp (no BoltDB/blobfs; creates config and backup dirs)
- [x] Step 2.2: Fix copyFile to preserve file permissions (stat src, OpenFile with src mode)
- [x] Step 2.3: Keep transformPath, isExist; added filePair type, mergePairs helper

### Phase 3: Init Command - done
- [x] Step 3.1: Implement new Init (--destination flag, validates base/, no --branch, writes dman.json)

### Phase 4: Apply Command - done
- [x] Step 4.1: Implement new Apply (base + profile overlay, per-file backup, optional scripts, dry-run)

### Phase 5: Add Command - done
- [x] Step 5.1: Implement new Add (profile-aware destination, git add/commit/push)

### Phase 6: git.go Cleanup - done
- [x] Step 6.1: Removed ListBranches, CurrentBranch, Checkout, CheckoutNewBranch, PushNewBranch; kept Pull, Add, Commit, Push, cloneRepo

### Phase 7: cmd/main.go Update - done
- [x] Step 7.1: Wired init/apply/add/pull/push/purge with new flags; removed backup/restore/snapshots/list/cat/env/migrate commands

### Phase 8: Tests - done
- [x] Step 8.1: Rewrite dman_test.go (TestTransformPath, TestBackupName, TestMergePairs, TestErrNoConfig)
- [x] Step 8.2: Write config_test.go (TestSaveReadConfig_RoundTrip, TestReadConfig_ErrNoConfig, TestSaveConfig_CreatesJSON)

### Phase 9: Final Verification - done
- [x] Step 9.1: go build ./... — PASS
- [x] Step 9.2: go vet ./... — PASS
- [x] Step 9.3: go test ./... -count=1 — PASS

## Files Modified
- `snapshot.go` - deleted
- `db.go` - deleted
- `db_test.go` - deleted
- `config.go` - rewritten — new Config struct, dman.json, ErrNoConfig
- `dman.go` - rewritten — new App struct, NewApp, copyFile (perms-preserving), transformPath, filePair, mergePairs
- `commands.go` - rewritten — Init, Apply, Add, Pull, Push, Purge; collectDotfiles, collectProfileDotfiles, backupFile, backupName, runScriptDir helpers
- `git.go` - cleaned up — removed branch-related methods; kept Pull, Add, Commit, Push, cloneRepo
- `cmd/main.go` - rewritten — urfave/cli/v3; init/apply/add/pull/push/purge commands with new flags
- `dman_test.go` - rewritten — table-driven tests for transformPath, backupName, mergePairs
- `config_test.go` - created — round-trip saveConfig/readConfig tests
- `go.mod` - updated — removed bbolt/blobfs; added urfave/cli/v3

## Decisions
| When | Question | Decision | Why |
|------|----------|----------|-----|
| Phase 0 | bbolt/blobfs removal | Deleted entirely, no migration | Plan explicitly states breaking changes allowed |
| Phase 2 | filePair location | Defined in dman.go | Co-located with App; used by commands.go helpers |
| Phase 5 | Add auto-push | Included in Add | Plan specifies git add/commit/push in Add flow |

## Verification Log
- [Phase 0] `go build ./...` — PASS
- [Phase 0] `go mod tidy` — PASS (bbolt/blobfs removed)
- [Phase 8] `go test ./... -count=1` — PASS
- [Final] `go build ./...` — PASS
- [Final] `go test ./... -count=1` — PASS

## Blockers
(none)
