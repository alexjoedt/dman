package dman

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v3"
)

type restoreCommand struct {
	name  string
	usage string

	// Flags
	dryRun bool
}

func RestoreCommand() *restoreCommand {
	return &restoreCommand{
		name:  "restore",
		usage: "restore dotfiles from a specific snapshot",
	}
}

func (r *restoreCommand) Name() string {
	return r.name
}

func (r *restoreCommand) Usage() string {
	return r.usage
}

func (r *restoreCommand) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{
			Name:        "dry-run",
			Usage:       "show what would be restored without making changes",
			Destination: &r.dryRun,
		},
	}
}

func (rcmd *restoreCommand) Action(ctx context.Context, c *cli.Command) error {
	snapshotID := c.Args().Get(0)
	if snapshotID == "" {
		return fmt.Errorf("snapshot ID required")
	}

	if err := validateShortID(snapshotID); err != nil {
		return fmt.Errorf("invalid snapshot ID: %w", err)
	}

	db, err := openDB()
	if err != nil {
		return fmt.Errorf("restore: open database: %w", err)
	}
	defer db.Close()

	// Get dotfiles from the snapshot
	dotfiles, err := listDotfilesBySnapshot(db, []byte(snapshotID))
	if err != nil {
		return fmt.Errorf("restore: get dotfiles from snapshot: %w", err)
	}

	if len(dotfiles) == 0 {
		return fmt.Errorf("no dotfiles found in snapshot %s", snapshotID[:12])
	}

	if rcmd.dryRun {
		fmt.Printf("Would restore %d dotfiles from snapshot %s:\n\n", len(dotfiles), snapshotID[:12])
		for _, dotfile := range dotfiles {
			fmt.Printf("  %s\n", dotfile.Name)
		}
		return nil
	}

	// Create a backup snapshot before restoring
	config, err := readConfig()
	if err != nil {
		return fmt.Errorf("restore: read config: %w", err)
	}

	mapping, err := getDotfiles(config.Path)
	if err != nil {
		return fmt.Errorf("restore: get current dotfiles: %w", err)
	}

	err = createSnapshot(db, mapping.GetFilesFromHome(), "before-restore")
	if err != nil {
		return fmt.Errorf("restore: create backup snapshot: %w", err)
	}

	// Restore each dotfile
	restored := 0
	for _, dotfile := range dotfiles {
		if err := restoreDotfile(dotfile); err != nil {
			fmt.Printf("Warning: failed to restore %s: %v\n", dotfile.Name, err)
			continue
		}
		fmt.Printf("Restored: %s\n", dotfile.Name)
		restored++
	}

	fmt.Printf("\nSuccessfully restored %d/%d dotfiles from snapshot %s\n", restored, len(dotfiles), snapshotID[:12])
	return nil
}

// restoreDotfile restores a single dotfile from snapshot data to its original location
func restoreDotfile(dotfile *Dotfile) error {
	// Ensure the directory exists
	dir := filepath.Dir(dotfile.Name)
	if err := os.MkdirAll(dir, HomeDir().Mode()); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	// Write the dotfile data to the target location
	if err := os.WriteFile(dotfile.Name, dotfile.Data, 0o644); err != nil {
		return fmt.Errorf("write file %s: %w", dotfile.Name, err)
	}

	return nil
}
