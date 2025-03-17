package dman

import (
	"context"

	"github.com/urfave/cli/v3"
)

type backupCommand struct {
	name  string
	usage string

	// Flags
	tags []string
}

func BackupCommand() *backupCommand {
	return &backupCommand{
		name:  "backup",
		usage: "creates a snapshot of the current dotfiles in the home directory",
	}
}

func (i *backupCommand) Name() string {
	return i.name
}

func (i *backupCommand) Usage() string {
	return i.usage
}

func (i *backupCommand) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringSliceFlag{
			Name:        "tag",
			Usage:       "add tags to the backup",
			Destination: &i.tags,
		},
	}
}

func (icmd *backupCommand) Action(ctx context.Context, c *cli.Command) error {

	db, err := openDB()
	if err != nil {
		return err
	}
	config, err := readConfig()
	if err != nil {
		return err
	}

	m, err := getDotfiles(config.Path)
	if err != nil {
		return err
	}

	return createSnapshot(db, m.GetFilesFromHome(), icmd.tags...)
}
