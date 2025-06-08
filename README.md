# dman

A simple but powerful dotfile manager that helps you manage your dotfiles across different environments using Git repositories and snapshots.

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

## Getting Started

### 1. Initialize dman with your dotfiles repository

```bash
# Initialize with a Git repository
dman init https://github.com/yourusername/dotfiles.git

# Initialize with a specific branch
dman init --branch work https://github.com/yourusername/dotfiles.git

# Initialize with custom destination
dman init --destination /path/to/dotfiles https://github.com/yourusername/dotfiles.git
```

### 2. Add dotfiles to your repository

```bash
# Add single dotfile
dman add ~/.zshrc

# Add multiple dotfiles
dman add ~/.vimrc ~/.gitconfig ~/.tmux.conf
```

### 3. Apply dotfiles from repository to home directory

```bash
# Apply all dotfiles
dman apply

# Dry run to see what would be applied
dman apply --dry-run
```

## Commands

### `init`
Initialize dman with a Git repository containing your dotfiles.

**Usage:**
```bash
dman init [OPTIONS] <repository-url>
```

**Options:**
- `--branch, -b`: Specify branch to clone (default: repository default)
- `--destination, -d`: Custom destination path (default: `~/.local/share/dman`)

**Examples:**
```bash
dman init https://github.com/user/dotfiles.git
dman init --branch work https://github.com/user/dotfiles.git
dman init -d ~/my-dotfiles https://github.com/user/dotfiles.git
```

### `add`
Add dotfiles from your home directory to the repository.

**Usage:**
```bash
dman add <file1> [file2] [file3] ...
```

**Examples:**
```bash
dman add ~/.zshrc
dman add ~/.vimrc ~/.gitconfig ~/.tmux.conf
dman add ~/.config/nvim/init.vim
```

**Note:** Files are automatically committed to Git with descriptive messages.

### `apply`
Apply dotfiles from the repository to your home directory.

**Usage:**
```bash
dman apply [OPTIONS]
```

**Options:**
- `--dry-run`: Show what would be applied without making changes
- `--exclude`: Exclude specific files (not implemented)
- `--include`: Include only specific files (not implemented)

**Examples:**
```bash
# Apply all dotfiles
dman apply

# See what would be applied
dman apply --dry-run
```

**Note:** Creates a snapshot before applying changes for backup purposes. Existing files are overwritten without prompt - use `--dry-run` first to preview changes.

### `backup`
Create a snapshot of current dotfiles in your home directory.

**Usage:**
```bash
dman backup [OPTIONS]
```

**Options:**
- `--tag, -t`: Add tags to the backup

**Examples:**
```bash
# Create a backup
dman backup

# Create a tagged backup
dman backup --tag "before-update" --tag "stable"
```

### `snapshots`
List all snapshots.

**Usage:**
```bash
dman snapshots [OPTIONS]
```

**Options:**
- `--tag, -t`: Filter by tags (not fully implemented)

**Examples:**
```bash
dman snapshots
```

**Output:**
```
ID            DATE                     TAGS
--            ----                     ----
dd554c7c4c1a  2023-12-01T10:30:00Z    [before-apply]
aa123b4c5d6e  2023-12-01T09:15:00Z    [manual-backup, stable]
```

### `list`
List dotfiles from snapshots or all dotfiles.

**Usage:**
```bash
dman list [OPTIONS] [snapshot-id]
```

**Options:**
- `--all, -a`: List all dotfiles across all snapshots

**Examples:**
```bash
# List dotfiles in a specific snapshot
dman list dd554c7c4c1a

# List all dotfiles
dman list --all
```

**Output:**
```
ID            NAME
--            ----
1a2b3c4d5e6f  /Users/user/.zshrc
7g8h9i0j1k2l  /Users/user/.vimrc
```

### `cat`
Display the content of a dotfile from a snapshot.

**Usage:**
```bash
dman cat <dotfile-id>
```

**Examples:**
```bash
dman cat 1a2b3c4d5e6f
```

### `env`
Manage environments (Git branches) for different dotfile configurations.

**Usage:**
```bash
dman env <subcommand> [args]
```

**Subcommands:**
- `list`: List all available environments
- `current`: Show current environment
- `switch <name>`: Switch to an environment
- `create <name>`: Create a new environment

**Examples:**
```bash
# List all environments
dman env list

# Show current environment  
dman env current

# Switch to work environment
dman env switch work

# Create and switch to new environment
dman env create personal
```

**Output for `env list`:**
```
Available environments:
* main (current)
  work
  personal
```

### `purge`
Remove all dman files and configuration.

**Usage:**
```bash
dman purge
```

**Examples:**
```bash
dman purge
```

**Warning:** This removes all configuration, database, and repository files. Use with caution.

## Configuration

dman stores its configuration in `~/.config/dman/config`:

```json
{
  "repository": "https://github.com/user/dotfiles.git",
  "branch": "main", 
  "path": "/Users/user/.local/share/dman"
}
```

## File Structure

```
~/.config/dman/
├── config          # Configuration file
└── dman.db         # Snapshot database

~/.local/share/dman/    # Default repository location
├── dot_zshrc          # ~/.zshrc
├── dot_vimrc          # ~/.vimrc  
└── dot_config/        # ~/.config/
    └── nvim/
        └── init.vim
```

## Workflow Examples

### Daily Workflow
```bash
# Start of day - apply latest dotfiles
dman apply

# Make changes to dotfiles...

# Add new or modified dotfiles
dman add ~/.zshrc ~/.vimrc

# Create backup before major changes
dman backup --tag "before-update"
```

### Environment Management
```bash
# Create work environment
dman env create work

# Switch between environments
dman env switch personal
dman env switch work

# List environments
dman env list
```

### Backup and Restore
```bash
# Create tagged backup
dman backup --tag "stable-config"

# List snapshots to find backup
dman snapshots

# View specific snapshot contents
dman list dd554c7c4c1a

# View file content from snapshot
dman cat 1a2b3c4d5e6f
```

## File Naming Convention

dman transforms dotfiles for storage in the repository:
- `~/.zshrc` → `dot_zshrc`
- `~/.config/nvim/init.vim` → `dot_config/nvim/init.vim`
- `~/.ssh/config` → `dot_ssh/config`

## Database

dman uses BoltDB to store snapshots and dotfile metadata locally at `~/.config/dman/dman.db`. This allows for efficient backup and restore operations without relying on Git history.

## Development

### Building
```bash
task build
```

### Testing
```bash
task test
```

### Cleaning
```bash
task clean
```

