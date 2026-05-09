# dman

A dotfile manager built around a Git repository and a base/profile overlay model. It applies dotfiles to the home directory, optionally runs setup scripts, installs packages, creates workspace directories, and clones repositories — all from a single declarative source.

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

Dotfiles are stored in a Git repository with a mandatory `base/` directory and optional per-machine profiles under `profiles/`. When applying, `base/` is always applied first; the active profile is merged on top, overriding any file that appears in both.

```
dotfiles/
  base/               # applied on every machine
  profiles/
    work/             # layered on top of base on work machines
    personal/         # layered on top of base on personal machines
  manifest.toml       # optional: packages, dirs, repos
```

### File naming convention

Files inside `base/` and `profiles/<name>/` use the same names as in the home directory, but with the leading dot replaced by the `dot_` prefix. Subdirectory structure is preserved.

```
~/.zshrc                    -> base/dot_zshrc
~/.config/nvim/init.lua     -> base/dot_config/nvim/init.lua
~/.ssh/config               -> base/dot_ssh/config
```

### Configuration

dman stores its runtime configuration at `~/.config/dman/dman.json`:

```json
{
  "repositoryURL": "https://github.com/user/dotfiles.git",
  "profile": "default",
  "path": "/Users/user/.local/share/dman"
}
```

This file is written by `dman init` and does not need to be edited manually.

### Backups

Before overwriting a file during `apply`, dman writes a timestamped backup to `~/.local/state/dman/backups/`.

---

## Setting up a dotfiles repository

If you do not have a dotfiles repository yet, create one and give it the required structure before running `dman init`.

**1. Create the repository on GitHub (or any Git host)**

**2. Clone it locally and create the required layout**

```bash
git clone git@github.com:youruser/dotfiles.git
cd dotfiles
mkdir -p base profiles/default
```

**3. Add your dotfiles**

Copy files into `base/` using the `dot_` naming convention:

```bash
cp ~/.zshrc base/dot_zshrc
cp ~/.gitconfig base/dot_gitconfig
mkdir -p base/dot_config/nvim
cp ~/.config/nvim/init.lua base/dot_config/nvim/init.lua
```

**4. Add an optional manifest**

Create `manifest.toml` in the repository root to declare packages, directories, and repositories that should be present on every machine:

```toml
[packages]
brew   = ["ripgrep", "fd", "fzf", "starship"]
apt    = ["ripgrep", "fd-find", "fzf"]
pacman = ["ripgrep", "fd", "fzf", "starship"]

[dirs]
paths = [
  "~/dev",
  "~/dev/personal",
  "~/dev/work",
]

[[repos]]
url  = "git@github.com:youruser/dotfiles.git"
dest = "~/dev/personal/dotfiles"
```

**5. Commit and push**

```bash
git add .
git commit -m "initial dotfiles"
git push
```

---

## Getting started on a new machine

```bash
# 1. Install dman
go install github.com/alexjoedt/dman/cmd@latest

# 2. Clone and initialise your dotfiles repository
dman init https://github.com/youruser/dotfiles.git

# 3. Install packages, create dirs, clone repos (requires manifest.toml)
dman setup

# 4. Apply dotfiles to the home directory
dman apply --run-scripts
```

---

## Commands

### `init`

Clones the dotfiles repository and writes the dman configuration. The repository must contain a `base/` directory.

```
dman init [--destination <path>] <repository-url>
```

**Flags:**
- `--destination, -d`: local path to clone into (default: `~/.local/share/dman`)

```bash
dman init https://github.com/user/dotfiles.git
dman init --destination ~/dotfiles https://github.com/user/dotfiles.git
```

---

### `apply`

Pulls from the remote, then copies files from `base/` and the active profile to the home directory. Only files that differ from the destination are written. Existing files are backed up before being overwritten.

```
dman apply [--profile <name>] [--dry-run] [--run-scripts]
```

**Flags:**
- `--profile, -p`: profile to apply (overrides the profile stored in config)
- `--dry-run`: print what would change without writing anything
- `--run-scripts`: run executable files found in `base/scripts/` and `profiles/<name>/scripts/` after applying

```bash
dman apply
dman apply --profile work --run-scripts
dman apply --dry-run
```

---

### `add`

Copies a dotfile from the home directory into the repository, then commits and pushes. By default the file goes into `base/`. Use `--profile` to target a specific profile instead.

```
dman add [--profile <name>] <file> [<file>...]
```

**Flags:**
- `--profile, -p`: add to this profile instead of base

```bash
dman add ~/.zshrc
dman add ~/.vimrc ~/.gitconfig ~/.tmux.conf
dman add --profile work ~/.config/nvim/init.lua
```

---

### `setup`

Reads `manifest.toml` from the dotfiles repository root and performs three steps in order:

1. **Packages** — detects the available package manager (`brew`, `yay`, `paru`, `pacman`, or `apt-get`) and installs the declared package list for that manager.
2. **Dirs** — creates each declared directory if it does not already exist. Tilde paths are expanded.
3. **Repos** — clones each declared repository to its destination. If the destination directory already exists and is non-empty, the clone is skipped.

All steps are idempotent and safe to re-run.

```
dman setup [--dry-run]
```

**Flags:**
- `--dry-run`: print each action without executing it

```bash
dman setup
dman setup --dry-run
```

**manifest.toml reference:**

```toml
[packages]
brew   = ["ripgrep", "fd", "fzf"]
apt    = ["ripgrep", "fd-find", "fzf"]
pacman = ["ripgrep", "fd", "fzf"]

[dirs]
paths = ["~/dev", "~/projects"]

[[repos]]
url  = "git@github.com:user/repo.git"
dest = "~/dev/repo"
```

---

### `pull`

Pulls the latest changes from the remote repository without applying them.

```bash
dman pull
```

---

### `push`

Pushes local commits to the remote repository.

```bash
dman push
```

---

### `purge`

Removes the dman configuration directory and the local clone of the dotfiles repository. Asks for confirmation before proceeding.

```bash
dman purge
```

---

## Workflow examples

### Day-to-day

```bash
# Apply latest dotfiles from remote
dman apply

# Edit a dotfile, then track it
dman add ~/.zshrc

# Push all pending commits
dman push
```

### Profile-based machines

```bash
# Work machine: apply base + work profile
dman apply --profile work

# Personal machine: apply base + personal profile
dman apply --profile personal

# Add a file to the work profile only
dman add --profile work ~/.config/work-tool/config
```

### Scripts

Place executable shell scripts in `base/scripts/` or `profiles/<name>/scripts/`. They are run in lexicographic order when `--run-scripts` is passed to `apply`. Use them for one-time setup tasks that cannot be expressed as file copies (enabling systemd user services, running `defaults write` on macOS, etc.).

```
base/
  scripts/
    01-macos-defaults.sh
    02-enable-services.sh
```

---

## Development

```bash
task build   # build binary to ./bin/dman
task test    # run tests
task install # build and install to $HOME/.local/bin
```

