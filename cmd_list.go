package dman

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/urfave/cli/v3"
)

type listCommand struct {
	name  string
	usage string
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
	return []cli.Flag{}
}

func (icmd *listCommand) Action(ctx context.Context, c *cli.Command) error {

	db, err := openDB()
	if err != nil {
		return err
	}

	id := c.Args().Get(0)
	if len(id) < 12 {
		return fmt.Errorf("invalid id '%s'", id)
	}

	snaps, err := listDotfiles(db, []byte(id))
	if err != nil {
		return err
	}

	printDotfileTable(snaps)

	return nil
}

func printDotfileTable(dotfiles []*Dotfile) {
	w := tabwriter.NewWriter(os.Stdout, 10, 0, 2, ' ', 0)
	fmt.Fprintf(w, "ID\tNAME\n")
	fmt.Fprintf(w, "--\t----\n")

	for _, d := range dotfiles {
		fmt.Fprintf(w, "%s\t%s\n", string(d.ID)[:12], d.Name)
	}

	w.Flush()
}
