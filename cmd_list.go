package dman

import (
	"context"
	"fmt"
	"os"
	"slices"
	"text/tabwriter"

	"github.com/urfave/cli/v3"
)

type listCommand struct {
	name  string
	usage string

	// Flags

	all bool
}

func ListCommand() *listCommand {
	return &listCommand{
		name:  "list",
		usage: "work with dotfiles",
	}
}

func (i *listCommand) Name() string {
	return i.name
}

func (i *listCommand) Usage() string {
	return i.usage
}

func (i *listCommand) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{
			Name:        "all",
			Aliases:     []string{"a"},
			Destination: &i.all,
		},
	}
}

func (icmd *listCommand) Action(ctx context.Context, c *cli.Command) error {

	db, err := openDB()
	if err != nil {
		return err
	}

	var dotfiles []*Dotfile
	if icmd.all {
		dotfiles, err = listAllDotfiles(db)
		if err != nil {
			return err
		}
	} else {
		id := c.Args().Get(0)
		if len(id) < 12 {
			return fmt.Errorf("invalid id '%s'", id)
		}
		dotfiles, err = listDotfilesBySnapshot(db, []byte(id))
		if err != nil {
			return err
		}
	}

	printDotfileTable(dotfiles)

	return nil
}

func printDotfileTable(dotfiles []*Dotfile) {
	w := tabwriter.NewWriter(os.Stdout, 10, 0, 2, ' ', 0)
	fmt.Fprintf(w, "ID\tNAME\tCREATED AT\n")
	fmt.Fprintf(w, "--\t------\t----------\n")

	slices.SortFunc(dotfiles, func(a *Dotfile, b *Dotfile) int {
		if a.CreatedAt.After(b.CreatedAt.Time) {
			return 1
		} else if b.CreatedAt.After(a.CreatedAt.Time) {
			return -1
		}
		return 0
	})
	for _, d := range dotfiles {
		fmt.Fprintf(w, "%s\t%s\t%s\n", string(d.ID)[:12], d.Name, d.CreatedAt.String())
	}

	w.Flush()
}
