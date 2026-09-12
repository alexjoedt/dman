# dman

[![CI](https://github.com/alexjoedt/dman/actions/workflows/ci.yml/badge.svg)](https://github.com/alexjoedt/dman/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/alexjoedt/dman.svg)](https://pkg.go.dev/github.com/alexjoedt/dman)
[![Go Report Card](https://goreportcard.com/badge/github.com/alexjoedt/dman)](https://goreportcard.com/report/github.com/alexjoedt/dman)
[![Latest release](https://img.shields.io/github/v/release/alexjoedt/dman)](https://github.com/alexjoedt/dman/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

> **Early stage project.** Expect rough edges and breaking changes.

A simple dotfile manager that does one thing: copy dotfiles between a Git repository and your home directory. No symlinks, no script execution, no templating.

If you need a comprehensive dotfile manager with templating, scripting, and broad platform support, consider [chezmoi](https://www.chezmoi.io/).

## Features

- Manages dotfiles from a Git repository using a `dot_`-prefixed naming convention
- Root/profile overlay model for per-machine overrides
- Automatic git add/commit/push on `add` and `apply`
- Snapshots of tracked dotfiles (basic, for peace of mind before applying)
- Optional per-file encryption with [age](https://age-encryption.org) for secrets like `~/.netrc` or `~/.ssh/config`

## Installation

### Arch Linux (AUR)
```bash
# Using an AUR helper
yay -S dman
# or
paru -S dman
```

The `dman` package installs the prebuilt binary from the [latest GitHub release](https://github.com/alexjoedt/dman/releases/latest).
The AUR metadata lives in [packaging/aur/](packaging/aur/).

### Using Go
```bash
go install github.com/alexjoedt/dman@latest
```

### Using Task
```bash
task install # copies the binary to $HOME/.local/bin/dman
```

### Manual Build
```bash
task build
cp ./bin/dman $HOME/.local/bin/dman
```

## Concepts

### Overlay model

Dotfiles are stored in a Git repository. The repository root is the base layer: any top-level `dot_*` entry is a tracked dotfile. Optional per-machine profiles live under `profiles/<name>/`. During apply, the root is loaded first and the selected profile overrides colliding files.

A simple repository needs nothing but `dot_*` files in its root. Profiles are entirely optional.

```
dotfiles/
  dot_zshrc
  dot_config/
    nvim/
  profiles/        # optional
    work/
    personal/
```

### Profile inheritance

A profile can declare one parent in `profiles/<name>/profile.json`:

```json
{
  "inherits": "arch"
}
```

The effective set is then root, followed by every ancestor from the top down, followed by the profile itself. Later layers win on colliding files, exactly like root vs profile. Chains can be arbitrarily deep; a missing parent or a cycle aborts the command.

```
dotfiles/
  dot_zshrc                          # root
  profiles/
    arch/
      dot_zshrc                      # overrides root
      dot_config/alacritty/...
    arch-work/
      profile.json                   # {"inherits": "arch"}
      dot_config/work.env           # only here
```

`dman apply --profile arch-work` applies root, then `arch`, then `arch-work`. Set the parent with `dman profiles inherit arch-work arch` or edit the file by hand. `profile.json` is never applied to `$HOME`.

### File naming convention

Files in the repository root and in `profiles/<name>/` use home path names with the leading dot replaced by `dot_`.

```
~/.zshrc                    -> dot_zshrc
~/.config/nvim/init.lua     -> dot_config/nvim/init.lua
~/.ssh/config               -> dot_ssh/config
~/.netrc (encrypted)        -> dot_netrc.crypt
```

The `.crypt` suffix is reserved: it marks a file whose content is stored encrypted. A plain file and its `.crypt` twin cannot coexist in the same layer.

### Encrypted dotfiles

Files added with `--encrypt` are stored as ASCII-armored [age](https://age-encryption.org) ciphertext under the `.crypt` suffix. A single age identity (private key) is all that is needed; the recipient is derived from it.

```
age-keygen -o ~/.config/age/key.txt
dman config encryption.age.identity ~/.config/age/key.txt
dman add --encrypt .netrc
```

Behavior:
- `dman add --encrypt <file>` writes `<name>.crypt`. If a plain copy exists in the repository it is replaced (and `git rm`'d when git automation is on).
- `dman add <file>` without the flag keeps a file encrypted if it is already stored as `.crypt`. Re-adding only rewrites the ciphertext when the plaintext changed.
- `dman add --sync --encrypt <dir>` encrypts every file in the tree.
- `dman apply` decrypts `.crypt` files and writes them with mode `0600`. Without a configured identity they are skipped and the summary reports how many.
- `dman sync` and the browse save action skip encrypted files with a warning when no identity is configured. `dman add` on such a file is an error.
- `dman diff` and `dman browse` show decrypted content on screen. Redirecting `dman diff` writes secrets to wherever it goes.
- Change detection always compares plaintext; age produces different ciphertext on every run.

Key resolution, in order: `DMAN_AGE_IDENTITY`, then `encryption.age.identity`. A passphrase-protected identity (`age -p -o key.txt.age key.txt`) is unlocked with `DMAN_AGE_PASSPHRASE` or an interactive prompt. Only native X25519 identities are supported (no SSH keys, no plugins). The decrypted identity stays in memory for the lifetime of the process.

### Configuration

dman stores runtime configuration at `~/.config/dman/dman.json`:

```json
{
  "repositoryURL": "https://github.com/user/dotfiles.git",
  "profile": "default",
  "path": "/Users/user/.local/share/dman",
  "git": {
    "autoAdd": false,
    "autoCommit": false,
    "autoPush": false
  },
  "snapshots": {
    "enabled": true,
    "path": "/Users/user/.local/state/dman/snapshots"
  },
  "encryption": {
    "age": {
      "identity": "~/.config/age/key.txt"
    }
  }
}
```

Notes:
- `git.autoAdd`, `git.autoCommit`, and `git.autoPush` default to `false`.
- `git.autoCommit: true` implicitly enables `git.autoAdd`.
- `git.autoPush: true` implicitly enables `git.autoCommit` and `git.autoAdd`.
- `snapshots.path` is optional.
- If `snapshots.path` is omitted, dman uses `~/.local/state/dman/snapshots`.
- If `snapshots` is omitted, snapshots are treated as enabled by default.
- `encryption.age.identity` is optional. Without it, encrypted files are skipped on `apply` and `--encrypt` fails. `DMAN_AGE_IDENTITY` overrides it.

## Setting up a dotfiles repository

Create a repository with at least one top-level `dot_*` entry (or a `profiles/` directory) before running `dman init`.

```bash
git clone git@github.com:youruser/dotfiles.git
cd dotfiles

cp ~/.zshrc dot_zshrc
cp ~/.gitconfig dot_gitconfig
mkdir -p dot_config/nvim
cp ~/.config/nvim/init.lua dot_config/nvim/init.lua

git add .
git commit -m "initial dotfiles"
git push
```

## Getting started on a new machine

```bash
# 1. Install dman
go install github.com/alexjoedt/dman@latest

# 2. Clone and initialize your dotfiles repository
dman init https://github.com/youruser/dotfiles.git

# 3. Apply dotfiles to the home directory
dman apply
```

## Commands

<!-- COMMAND_MATRIX_START -->
| Command | Args | Flags |
| --- | --- | --- |
| `dman init` | `<repo-url>` | `--destination`, `-d` |
| `dman apply` | `[file...]` | `--profile`, `-p`, `--dry-run`, `--no-pull`, `--no-snapshot` |
| `dman diff` | `[file...]` | `--profile`, `-p` |
| `dman browse` | `-` | `--profile`, `-p` |
| `dman add` | `<file> [<file>...]` | `--profile`, `-p`, `--encrypt`, `--sync`, `--dry-run`, `--add`, `--commit`, `--push` |
| `dman sync` | `-` | `--profile`, `-p`, `--dry-run`, `--add`, `--commit`, `--push` |
| `dman pull` | `-` | `-` |
| `dman push` | `-` | `-` |
| `dman cd` | `-` | `-` |
| `dman purge` | `-` | `-` |
| `dman version` | `-` | `-` |
| `dman profiles` | `-` | `-` |
| `dman profiles list` | `-` | `-` |
| `dman profiles set` | `<name>` | `-` |
| `dman profiles inherit` | `<child> <parent>` | `--clear` |
| `dman config` | `[<key> [<value>]]` | `--unset` |
| `dman config list` | `[profiles]` | `-` |
| `dman snapshot` | `-` | `-` |
| `dman snapshot list` | `-` | `-` |
| `dman snapshot create` | `[--message <text>]` | `--message`, `-m` |
| `dman snapshot show` | `<snapshot-id>` | `-` |
| `dman snapshot cat` | `<checksum>` | `-` |
| `dman snapshot restore` | `<snapshot-id> <file>...` | `-` |
| `dman snapshot delete` | `<snapshot-id>` | `-` |
<!-- COMMAND_MATRIX_END -->

### `init`

Clones the dotfiles repository and writes dman configuration. The repository must contain at least one top-level `dot_*` entry or a `profiles/` directory.

```
dman init [--destination <path>] <repository-url>
```

Flags:
- `--destination`, `-d`: local path to clone into (default: `~/.local/share/dman`)

### `apply`

Optionally pulls latest changes, merges the repository root with the selected profile, and copies changed files to `$HOME`. Encrypted (`.crypt`) files are decrypted and written with mode `0600` when an age identity is configured, otherwise skipped.

```
dman apply [--profile <name>] [--dry-run] [--no-pull] [--no-snapshot]
```

Flags:
- `--profile`, `-p`: profile to apply (overrides profile in config)
- `--dry-run`: print actions without writing files
- `--no-pull`: skip git pull before applying
- `--no-snapshot`: skip automatic pre-apply snapshot

### `add`

Copies dotfiles from `$HOME` into the repository. Git add/commit/push steps are controlled by config (`git.autoAdd`, `git.autoCommit`, `git.autoPush`) and can be enabled per invocation with flags. Directory inputs are walked recursively and binary files are skipped.

```
dman add [--profile <name>] [--encrypt] [--add] [--commit] [--push] <path> [<path>...]
dman add --sync <directory> [--profile <name>] [--encrypt] [--dry-run] [--add] [--commit] [--push]
```

Flags:
- `--profile`, `-p`: add to this profile instead of the repository root
- `--encrypt`: store the file(s) age-encrypted under the `.crypt` suffix (see "Encrypted dotfiles")
- `--sync`: sync from one directory and prune removed files from the matching repo subtree
- `--dry-run`: preview sync changes without writing, staging, or committing (only with `--sync`)
- `--add`: stage copied files in git
- `--commit`: create a commit for staged changes (implies add)
- `--push`: push committed changes to remote (implies commit and add)

### `sync`

Updates the repository from `$HOME` for every tracked dotfile in one step (home -> repo). It is the inverse of `apply`: the tracked set is defined entirely by the repository, so only files that already exist in the repo are updated, honoring the active profile overlay. Sync never deletes; tracked files missing from `$HOME` are skipped with a warning. When a file is tracked in more than one layer (base, a parent profile, the active profile), only the copy in the winning layer (the one apply would use) is updated. Git add/commit/push steps follow the same config and flags as `add`.

```
dman sync [--profile <name>] [--dry-run] [--add] [--commit] [--push]
```

Flags:
- `--profile`, `-p`: profile to sync (overrides profile in config)
- `--dry-run`: print actions without writing files
- `--add`: stage updated files in git
- `--commit`: create a commit for staged changes (implies add)
- `--push`: push committed changes to remote (implies commit and add)

### `profiles`

Lists profile directories in the repository; the active one is marked with `*` and inherited chains are shown nearest parent first.

```bash
dman profiles                          # or: dman profiles list
dman profiles set arch-work           # set the active profile
dman profiles inherit arch-work arch  # arch-work now inherits arch
dman profiles inherit arch-work --clear
```

`inherit` creates `profiles/<child>/` if it does not exist and refuses a parent that is missing or would form a cycle. It writes `profiles/<child>/profile.json` and leaves committing to you.

### `pull`

Pulls latest changes from the remote repository.

```bash
dman pull
```

### `push`

Pushes local commits to the remote repository.

```bash
dman push
```

### `cd`

Starts a shell in the local dotfiles repository path from config.

```bash
dman cd
```

Exit the shell to return to your previous directory.

### `purge`

Removes dman configuration and the local dotfiles clone after confirmation.

```bash
dman purge
```

### `version`

Prints the dman version.

```bash
dman version
```

### `snapshot`

Manages snapshots of tracked dotfiles.

#### `snapshot list`

Lists all snapshots.

```bash
dman snapshot list
```

#### `snapshot create`

Creates a snapshot of currently tracked dotfiles that exist on disk.

```bash
dman snapshot create [--message <text>]
```

#### `snapshot show`

Shows files in a snapshot.

```bash
dman snapshot show <snapshot-id>
```

#### `snapshot cat`

Prints file content by checksum (full checksum or unambiguous prefix).

```bash
dman snapshot cat <checksum>
```

#### `snapshot restore`

Restores the snapshot's version of the named files into the home directory.
Files whose current contents already match the snapshot are skipped, and
everything that will change is snapshotted first, so a restore is itself
undoable.

```bash
dman snapshot restore <snapshot-id> <file>...
```

At least one file is required; restoring a whole snapshot in one go is
deliberately not offered.

#### `snapshot delete`

Deletes a snapshot and reclaims unreferenced blobs.

```bash
dman snapshot delete <snapshot-id>
```

## Workflow examples

```bash
# Apply latest dotfiles from remote
dman apply

# Track local dotfile changes
dman add ~/.zshrc ~/.gitconfig
```

## Development

```bash
task build   # build binary to ./bin/dman
task test    # run tests
task install # build and install to $HOME/.local/bin/dman
```

## Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) for
development setup, conventions, and pull request guidelines. Security issues
should be reported following the [security policy](SECURITY.md).

## License

Released under the [MIT License](LICENSE).

