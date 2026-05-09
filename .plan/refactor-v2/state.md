# Execution: dman Refactor v2 — Overlay Architecture
**Source**: .plan/refactor-v2.md
**Status**: in_progress
**Started**: 2026-05-09T00:00:00Z
**Updated**: 2026-05-09T00:00:00Z

## Progress

### Phase 0: Delete Old Code - pending
- [ ] Step 0.1: Delete snapshot.go, db.go, db_test.go
- [ ] Step 0.2: Remove deprecated methods from commands.go (Backup, Cat, Restore, List, Snapshots, Env*, Migrate, GC)
- [ ] Step 0.3: Remove deprecated CLI commands from cmd/main.go
- [ ] Step 0.4: go mod tidy

### Phase 1: Config Rewrite - pending
- [ ] Step 1.1: Rewrite config.go (new Config struct, new filename dman.json)

### Phase 2: dman.go Rewrite - pending
- [ ] Step 2.1: Rewrite App struct and NewApp (no BoltDB/blobfs)
- [ ] Step 2.2: Fix copyFile to preserve file permissions
- [ ] Step 2.3: Keep transformPath, isExist

### Phase 3: Init Command - pending
- [ ] Step 3.1: Implement new Init (--destination flag, validates base/, no --branch)

### Phase 4: Apply Command - pending
- [ ] Step 4.1: Implement new Apply (base + profile overlay, backup, scripts)

### Phase 5: Add Command - pending
- [ ] Step 5.1: Implement new Add (profile-aware destination)

### Phase 6: git.go Cleanup - pending
- [ ] Step 6.1: Remove ListBranches, CurrentBranch, Checkout, CheckoutNewBranch, PushNewBranch

### Phase 7: cmd/main.go Update - pending
- [ ] Step 7.1: Wire new flags, remove old commands

### Phase 8: Tests - pending
- [ ] Step 8.1: Rewrite dman_test.go (transformPath, applyFiles, backupName)
- [ ] Step 8.2: Write config_test.go (round-trip saveConfig/readConfig)

### Phase 9: Final Verification - pending
- [ ] Step 9.1: go build ./...
- [ ] Step 9.2: go vet ./...
- [ ] Step 9.3: go test ./... -count=1

## Files Modified
(none yet)

## Decisions
| When | Question | Decision | Why |
|------|----------|----------|-----|

## Verification Log
(none yet)

## Blockers
(none)
