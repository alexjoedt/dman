# dman

A dotfile manager focused on two things:

1. Managing dotfiles from a Git repository with a base/profile overlay model.
2. Creating and inspecting snapshots of tracked dotfiles.

## Installation

### Using Go
```bash
go install github.com/alexjoedt/dman/cmd@latest
```

### Using Task
```bash
task install
```

### Manual Build
```bash
task build
cp ./bin/dman $HOME/.local/bin/dman
```

## Concepts

### Overlay model

Dotfiles are stored in a Git repository with a mandatory `base/` directory and optional per-machine profiles under `profiles/`. During apply, `base/` is loaded first and the selected profile overrides colliding files.

```
dotfiles/
  base/
  profiles/
    work/
    personal/
```

### File naming convention

Files in `base/` and `profiles/<name>/` use home path names with the leading dot replaced by `dot_`.

```
~/.zshrc                    -> base/dot_zshrc
~/.config/nvim/init.lua     -> base/dot_config/nvim/init.lua
~/.ssh/config               -> base/dot_ssh/config
```

### Configuration

dman stores runtime configuration at `~/.config/dman/dman.json`:

```json
{
  "repositoryURL": "https://github.com/user/dotfiles.git",
  "profile": "default",
  "path": "/Users/user/.local/share/dman",
  "snapshots": {
    "enabled": true,
    "path": "/Users/user/.local/state/dman/snapshots"
  }
}
```

Notes:
- `snapshots.path` is optional.
- If `snapshots.path` is omitted, dman uses `~/.local/state/dman/snapshots`.
- If `snapshots` is omitted, snapshots are treated as enabled by default.

## Setting up a dotfiles repository

Create a repository with at least `base/` before running `dman init`.

```bash
git clone git@github.com:youruser/dotfiles.git
cd dotfiles
mkdir -p base profiles/default

cp ~/.zshrc base/dot_zshrc
cp ~/.gitconfig base/dot_gitconfig
mkdir -p base/dot_config/nvim
cp ~/.config/nvim/init.lua base/dot_config/nvim/init.lua

git add .
git commit -m "initial dotfiles"
git push
```

## Getting started on a new machine

```bash
# 1. Install dman
go install github.com/alexjoedt/dman/cmd@latest

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
| `dman apply` | `-` | `--profile`, `-p`, `--dry-run`, `--no-pull`, `--no-snapshot` |
| `dman add` | `<file> [<file>...]` | `--profile`, `-p`, `--no-push` |
| `dman pull` | `-` | `-` |
| `dman push` | `-` | `-` |
| `dman purge` | `-` | `-` |
| `dman version` | `-` | `-` |
| `dman snapshot` | `-` | `-` |
| `dman snapshot list` | `-` | `-` |
| `dman snapshot create` | `[--message <text>]` | `--message`, `-m` |
| `dman snapshot show` | `<snapshot-id>` | `-` |
| `dman snapshot cat` | `<checksum>` | `-` |
| `dman snapshot delete` | `<snapshot-id>` | `-` |
<!-- COMMAND_MATRIX_END -->

### `init`

Clones the dotfiles repository and writes dman configuration. The repository must contain `base/`.

```
dman init [--destination <path>] <repository-url>
```

Flags:
- `--destination`, `-d`: local path to clone into (default: `~/.local/share/dman`)

### `apply`

Optionally pulls latest changes, merges `base/` with the selected profile, and copies changed files to `$HOME`.

```
dman apply [--profile <name>] [--dry-run] [--no-pull] [--no-snapshot]
```

Flags:
- `--profile`, `-p`: profile to apply (overrides profile in config)
- `--dry-run`: print actions without writing files
- `--no-pull`: skip git pull before applying
- `--no-snapshot`: skip automatic pre-apply snapshot

### `add`

Copies dotfiles from `$HOME` into the repository, stages them, commits, and pushes. Directory inputs are walked recursively and binary files are skipped.

```
dman add [--profile <name>] [--no-push] <path> [<path>...]
```

Flags:
- `--profile`, `-p`: add to this profile instead of base/default target
- `--no-push`: commit without pushing

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

# Create a manual snapshot
dman snapshot create --message "before shell refactor"

# Inspect snapshot contents
dman snapshot show <snapshot-id>
```

## Development

```bash
task build   # build binary to ./bin/dman
task test    # run tests
task install # build and install to $HOME/.grip/bin/dman
```

