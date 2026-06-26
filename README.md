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

### File naming convention

Files in the repository root and in `profiles/<name>/` use home path names with the leading dot replaced by `dot_`.

```
~/.zshrc                    -> dot_zshrc
~/.config/nvim/init.lua     -> dot_config/nvim/init.lua
~/.ssh/config               -> dot_ssh/config
```

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
| `dman add` | `<file> [<file>...]` | `--profile`, `-p`, `--sync`, `--dry-run`, `--add`, `--commit`, `--push` |
| `dman sync` | `-` | `--profile`, `-p`, `--dry-run`, `--add`, `--commit`, `--push` |
| `dman pull` | `-` | `-` |
| `dman push` | `-` | `-` |
| `dman cd` | `-` | `-` |
| `dman purge` | `-` | `-` |
| `dman version` | `-` | `-` |
| `dman profiles` | `-` | `-` |
| `dman profiles list` | `-` | `-` |
| `dman profiles set` | `<name>` | `-` |
| `dman config` | `[<key> [<value>]]` | `--unset` |
| `dman config list` | `[profiles]` | `-` |
| `dman snapshot` | `-` | `-` |
| `dman snapshot list` | `-` | `-` |
| `dman snapshot create` | `[--message <text>]` | `--message`, `-m` |
| `dman snapshot show` | `<snapshot-id>` | `-` |
| `dman snapshot cat` | `<checksum>` | `-` |
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

Optionally pulls latest changes, merges the repository root with the selected profile, and copies changed files to `$HOME`.

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
dman add [--profile <name>] [--add] [--commit] [--push] <path> [<path>...]
dman add --sync <directory> [--profile <name>] [--dry-run] [--add] [--commit] [--push]
```

Flags:
- `--profile`, `-p`: add to this profile instead of the repository root
- `--sync`: sync from one directory and prune removed files from the matching repo subtree
- `--dry-run`: preview sync changes without writing, staging, or committing (only with `--sync`)
- `--add`: stage copied files in git
- `--commit`: create a commit for staged changes (implies add)
- `--push`: push committed changes to remote (implies commit and add)

### `sync`

Updates the repository from `$HOME` for every tracked dotfile in one step (home -> repo). It is the inverse of `apply`: the tracked set is defined entirely by the repository, so only files that already exist in the repo are updated, honoring the active profile overlay. Sync never deletes; tracked files missing from `$HOME` are skipped with a warning. When a file is tracked in both the base and the active profile, only the profile copy (the one that wins on apply) is updated. Git add/commit/push steps follow the same config and flags as `add`.

```
dman sync [--profile <name>] [--dry-run] [--add] [--commit] [--push]
```

Flags:
- `--profile`, `-p`: profile to sync (overrides profile in config)
- `--dry-run`: print actions without writing files
- `--add`: stage updated files in git
- `--commit`: create a commit for staged changes (implies add)
- `--push`: push committed changes to remote (implies commit and add)

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

