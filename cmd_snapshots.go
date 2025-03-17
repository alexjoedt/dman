package dman

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/urfave/cli/v3"
)

type snapshotCommand struct {
	name  string
	usage string

	tag []string
}

func SnapshotCommand() *snapshotCommand {
	return &snapshotCommand{
		name:  "snapshots",
		usage: "work with snapshots",
	}
}

func (i *snapshotCommand) Name() string {
	return i.name
}

func (i *snapshotCommand) Usage() string {
	return i.usage
}

func (i *snapshotCommand) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringSliceFlag{
			Name:        "tag",
			Aliases:     []string{"t"},
			Destination: &i.tag,
		},
	}
}

func (icmd *snapshotCommand) Action(ctx context.Context, c *cli.Command) error {

	db, err := openDB()
	if err != nil {
		return err
	}

	snaps, err := listSnapshots(db)
	if err != nil {
		return err
	}

	printSnapshotTable(snaps)

	return nil
}

func printSnapshotTable(snapshots []*Snapshot) {
	w := tabwriter.NewWriter(os.Stdout, 10, 0, 2, ' ', 0)
	fmt.Fprintf(w, "ID\tDATE\tTAGS\n")
	fmt.Fprintf(w, "--\t----\t----\n")

	for _, s := range snapshots {
		fmt.Fprintf(w, "%s\t%s\t%v\n", string(s.ID)[:12], s.Date.String(), s.Tags)
	}
	w.Flush()
}
