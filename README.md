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
The AUR metadata lives in [packaging/aur/](packaging/aur/). The package also installs the bash, zsh and fish completions.

### Using Go
```bash
go install github.com/alexjoedt/dman@latest
```

The binary lands in `$(go env GOPATH)/bin`, usually `~/go/bin`, which must be on your `PATH`.

### Using Task
```bash
task install # copies the binary to $HOME/.local/bin/dman
```

### Manual Build
```bash
task build
cp ./bin/dman $HOME/.local/bin/dman
```

### Shell completion

The AUR package needs nothing. With `go install` or a manual build, add one line per shell:

```bash
source <(dman completion bash)                          # ~/.bashrc
source <(dman completion zsh)                           # ~/.zshrc, after compinit
mkdir -p ~/.config/fish/completions && dman completion fish > ~/.config/fish/completions/dman.fish
```

Completion covers subcommands and flags, not values such as profile names or snapshot IDs.

## Getting started on a new machine

No dotfiles repository yet? Start with [Setting up a dotfiles repository](#setting-up-a-dotfiles-repository).

1. Install dman from the AUR or with Go (see [Installation](#installation); with Go, `~/go/bin` must be on your `PATH`):

   ```bash
   go install github.com/alexjoedt/dman@latest
   ```

2. Clone your dotfiles repository. `init` lists the profiles it finds and prints the next commands:

   ```bash
   dman init https://github.com/youruser/dotfiles.git
   ```

3. Pick a profile, if the repository has any:

   ```bash
   dman profiles set <name>
   ```

4. If the repository holds encrypted (`.crypt`) files, copy your age identity from an existing machine (scp, a password manager, whatever you use) to `~/.config/age/key.txt`, the conventional path, and point dman at it:

   ```bash
   mkdir -p ~/.config/age
   # copy the key to ~/.config/age/key.txt, then:
   chmod 600 ~/.config/age/key.txt
   dman config encryption.age.identity ~/.config/age/key.txt
   ```

5. Copy the dotfiles into your home directory:

   ```bash
   dman apply
   ```

6. Check the setup. A `fail` line needs its fix. A `warn` line can be expected: `identity none configured` when you skipped step 4, `key protected, not verified` for a passphrase-protected identity, `profile default: no directory` in a repository without profiles:

   ```bash
   dman doctor
   ```

7. Enable completions (the AUR package already ships them): run or add the line for your shell from [Shell completion](#shell-completion) (zsh: after `compinit`), for bash:

   ```bash
   echo 'source <(dman completion bash)' >> ~/.bashrc
   ```

   If dman tracks that rc file, `dman sync --push` commits the change and pushes it; plain `dman sync` only updates the local clone.

Open a new shell to pick up the applied files.

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

### Standalone profiles

A profile marked standalone drops the repository root from its effective set, so only the profile (and its ancestors) are applied:

```json
{
  "standalone": true
}
```

The mark carries down the chain: if any profile in the chain is standalone, the root is skipped. Apply, diff, sync, browse and snapshots all follow the same set. Set it with `dman profiles standalone <name>` or edit the file by hand. While a standalone profile is active, `dman add` stores new files in it instead of the root; pass `--root` to add to the root anyway. dman never deletes files from `$HOME`, so root files applied before a profile became standalone stay in place.

A standalone profile can keep part of the root with a hand-edited `root` list:

```json
{
  "standalone": true,
  "root": ["~/.config/nvim", "~/.zshrc"]
}
```

Only root files at or below an entry are applied, diffed, synced, browsed and snapshotted; every other root file stays out. Entries are written in the `~/` form, match by path prefix (no globs) and are unioned over the inheritance chain, so a child of a standalone profile gets its ancestors' entries too. An entry that matches nothing is ignored; an empty entry or a bare `~` is an error. `root` is ignored (flagged in `dman profiles list`) when the chain is not standalone, and `dman profiles standalone --clear` leaves the list in place. `dman add` is unchanged: new files still go to the active standalone profile, and a profile copy overrides the root copy of the same file.

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
- `dman add --encrypt <file>` writes `<name>.crypt`. If a plain copy exists in the repository it is replaced (and `git rm`'d when git automation is on). The old plaintext stays in git history: treat the secret as exposed and rotate it.
- `dman add <file>` without the flag encrypts the file when any layer of the repository (the root or any profile) stores it as `.crypt`, and logs which one. A plain copy in the target layer is replaced the same way as with `--encrypt`. Re-adding only rewrites the ciphertext when the plaintext changed.
- `dman add --encrypt <dir>` encrypts every file in the tree.
- `dman apply` decrypts `.crypt` files and writes them with mode `0600`. Without a configured identity they are skipped and the summary reports how many.
- `dman sync` and the browse save action skip encrypted files with a warning when no identity is configured. `dman add` on such a file is an error.
- `dman diff` and `dman browse` show decrypted content on screen. Redirecting `dman diff` writes secrets to wherever it goes.
- Change detection always compares plaintext; age produces different ciphertext on every run.

Key resolution, in order: `DMAN_AGE_IDENTITY`, then `encryption.age.identity`. A passphrase-protected identity (`age -p -o key.txt.age key.txt`) is unlocked with `DMAN_AGE_PASSPHRASE` or an interactive prompt. Only native X25519 identities are supported (no SSH keys, no plugins). The decrypted identity stays in memory for the lifetime of the process.

### Configuration

dman stores runtime configuration at `$XDG_CONFIG_HOME/dman/dman.json`, or `~/.config/dman/dman.json` when `XDG_CONFIG_HOME` is not set:

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
- If `snapshots.path` is omitted, dman uses `$XDG_STATE_HOME/dman/snapshots`, or `~/.local/state/dman/snapshots` when `XDG_STATE_HOME` is not set.
- If `snapshots` is omitted, snapshots are treated as enabled by default.
- `encryption.age.identity` is optional. Without it, encrypted files are skipped on `apply` and `--encrypt` fails. `DMAN_AGE_IDENTITY` overrides it.

XDG directories:
- `XDG_CONFIG_HOME` holds `dman/dman.json`, `XDG_STATE_HOME` holds `dman/snapshots`, and `XDG_DATA_HOME` is where `init` clones to by default (`dman/`).
- An unset, empty or relative value falls back to `~/.config`, `~/.local/state` and `~/.local/share`.
- An existing setup keeps working: when `dman.json` (or the snapshots directory) exists only under the old path, dman uses it in place and moves nothing. When both exist, the XDG one wins.
- The clone path is stored in `dman.json`, so `XDG_DATA_HOME` only matters for `init`.
- dman creates the config directory when it first saves the config.

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

## Commands

<!-- COMMAND_MATRIX_START -->
| Command | Args | Flags |
| --- | --- | --- |
| `dman init` | `<repo-url>` | `--destination`, `-d` |
| `dman apply` | `[file...]` | `--profile`, `-p`, `--dry-run`, `--no-pull`, `--no-snapshot` |
| `dman diff` | `[file...]` | `--profile`, `-p` |
| `dman browse` | `-` | `--profile`, `-p` |
| `dman add` | `<file> [<file>...]` | `--profile`, `-p`, `--root`, `--encrypt`, `--add`, `--commit`, `--push` |
| `dman sync` | `-` | `--profile`, `-p`, `--dry-run`, `--add`, `--commit`, `--push` |
| `dman pull` | `-` | `-` |
| `dman push` | `-` | `-` |
| `dman purge` | `-` | `-` |
| `dman doctor` | `-` | `-` |
| `dman version` | `-` | `-` |
| `dman profiles` | `-` | `-` |
| `dman profiles list` | `-` | `-` |
| `dman profiles set` | `<name>` | `-` |
| `dman profiles inherit` | `<child> <parent>` | `--clear` |
| `dman profiles standalone` | `<name>` | `--clear` |
| `dman config` | `[<key> [<value>]]` | `--unset` |
| `dman config list` | `[profiles]` | `-` |
| `dman snapshot` | `-` | `-` |
| `dman snapshot list` | `-` | `-` |
| `dman snapshot create` | `[--message <text>]` | `--message`, `-m` |
| `dman snapshot show` | `<snapshot-id>` | `-` |
| `dman snapshot cat` | `<checksum>` | `-` |
| `dman snapshot restore` | `<snapshot-id> [file...]` | `-` |
| `dman snapshot delete` | `<snapshot-id>` | `-` |
<!-- COMMAND_MATRIX_END -->

### `init`

Clones the dotfiles repository and writes dman configuration. The repository must contain at least one top-level `dot_*` entry or a `profiles/` directory. Afterwards it prints the next steps: the profile names and `dman profiles set <name>` when the repository has profiles, `dman config encryption.age.identity <path>` when it holds `.crypt` files (unless `DMAN_AGE_IDENTITY` is set), then `dman apply` and `dman doctor`.

```
dman init [--destination <path>] <repository-url>
```

Flags:
- `--destination`, `-d`: local path to clone into (default: `$XDG_DATA_HOME/dman`, or `~/.local/share/dman` when `XDG_DATA_HOME` is not set)

### `apply`

Optionally pulls latest changes, merges the repository root with the selected profile, and copies changed files to `$HOME`. Encrypted (`.crypt`) files are decrypted and written with mode `0600` when an age identity is configured, otherwise skipped. If a write fails midway, the files already written are restored from the automatic pre-apply snapshot and files it created are removed; directories it created stay (with `--no-snapshot` or snapshots disabled the written files are left in place).

```
dman apply [--profile <name>] [--dry-run] [--no-pull] [--no-snapshot]
```

Flags:
- `--profile`, `-p`: profile to apply (overrides profile in config)
- `--dry-run`: print actions without writing files
- `--no-pull`: skip git pull before applying
- `--no-snapshot`: skip automatic pre-apply snapshot

### `add`

Copies dotfiles from `$HOME` into the repository. Git add/commit/push steps are controlled by config (`git.autoAdd`, `git.autoCommit`, `git.autoPush`) and can be enabled per invocation with flags. Directory inputs are walked recursively and binary files are skipped. `add` only copies: files deleted from a directory in `$HOME` are not removed from the repository, delete them there by hand.

```
dman add [--profile <name> | --root] [--encrypt] [--add] [--commit] [--push] <path> [<path>...]
```

Flags:
- `--profile`, `-p`: add to this profile instead of the repository root (default: the active profile if it is standalone, otherwise the root)
- `--root`: add to the repository root even if the active profile is standalone; cannot be combined with `--profile`
- `--encrypt`: store the file(s) age-encrypted under the `.crypt` suffix (see "Encrypted dotfiles"). Without the flag a file is still encrypted when any layer of the repository stores it as `.crypt`. Replacing a plain copy leaves the old plaintext in git history; rotate the secret
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

Lists profile directories in the repository; the active one is marked with `*`, inherited chains are shown nearest parent first, and standalone profiles are tagged `(standalone)`.

```bash
dman profiles                          # or: dman profiles list
dman profiles set arch-work           # set the active profile
dman profiles inherit arch-work arch  # arch-work now inherits arch
dman profiles inherit arch-work --clear
dman profiles standalone server       # apply server without the root
dman profiles standalone server --clear
```

`inherit` creates `profiles/<child>/` if it does not exist and refuses a parent that is missing or would form a cycle. `standalone` creates `profiles/<name>/` the same way. Both write `profiles/<name>/profile.json`, keep the other setting in it, and leave committing to you.

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

Removes `dman.json` and the local dotfiles clone after confirmation. A legacy `~/.config/dman/dman.json` shadowed by one under `XDG_CONFIG_HOME` is removed too. A config directory is removed only when nothing else is left in it. The clone is removed when it lies inside your home directory or at the default `init` destination. The snapshot store is kept.

```bash
dman purge
```

### `doctor`

Checks the local setup and prints one `ok`, `warn` or `fail` line per check, with a hint naming the fix on every line that is not `ok`: dman version, config path, `git`, repository path and remote, active profile and its inheritance chain, age identity (path and whether it comes from `DMAN_AGE_IDENTITY` or the config), snapshot store path and count, and a legacy config or snapshot directory shadowed by the XDG one.

```bash
dman doctor
```

- Exits 1 when a check fails, 0 otherwise; warnings (no identity, no remote, snapshots disabled, protected identity not verified) do not change the exit code.
- Without a config it prints the version, the config path, the `git` check and the `dman init <repo-url>` hint, then exits 1.
- With an identity and at least one `.crypt` file in the effective set, it decrypts one of them in memory. A passphrase-protected identity is verified only when `DMAN_AGE_PASSPHRASE` is set; doctor never prompts.
- It writes nothing: no config or snapshot directory, no snapshot, no git network call.

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

Restores the snapshot's version of the named files, or of every file in the
snapshot when none are named, into the home directory.
Files the snapshot recorded as absent (they did not exist when it was taken,
for example files created by `apply`) are deleted; their parent directories
stay. Files whose current contents already match the snapshot are skipped, and
everything that will change is snapshotted first, so a restore is itself
undoable. If a write fails midway, the files already restored are put back
from that pre-restore snapshot.

```bash
dman snapshot restore <snapshot-id> [file...]
```

There is no confirmation prompt: the step log shows what changes, and
the pre-restore snapshot lets you undo a full restore with
`dman snapshot restore <new-snapshot-id>`.

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

# Jump into the local repository (add to your shell rc)
alias dcd='cd "$(dman config path)"'
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

