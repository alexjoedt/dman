# Sratchpad


## Ideas

- `diff` command to show diff of current file and file to apply

## Improvements
- Refactor project structure (e.g commands in cmd - main in root?)
- Improve environment handling?
- Add install scripts like chezmoi? (its to much for a simple dotfile manager IMHO)

## Snapshots

Create a feature that creates local snapshots of the current state of the local dotfiles.

For example: when the user runs `dman apply` all local files will be overwritten. If the user had made a local change in .zshrc the change is lost.
I want that the user can configure to enable snapshots in the dman config json that before a file is overwritten dman creates a snapshot of files that are target by dman apply before overwriting any file.
The location can be ~/.local/share/dman-snapshots - this should be a CAS storage using github.com/alexjoedt/blobfs and also create an index to be able to list snapshots, restore snapshots view files from snapshots.
For example:

```bash
dman snapshots # lists all snapshots, every snapshot has a unique id uuid v7
dman backup # creates a snapshot of the local dotfiles (only backup local files that are listed in the dman dotfiles)
dman list <snapshot-id> # list all files in a snapshot (every file as a unique id its sha256 checksum)
dman cat <checksum> # prints the content of a file to stdout

```