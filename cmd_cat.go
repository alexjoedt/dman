package dman

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

type catCommand struct {
	name  string
	usage string
}

func (c *catCommand) Name() string {
	return c.name
}

func (c *catCommand) Usage() string {
	return c.usage
}

func (c *catCommand) Flags() []cli.Flag {
	return []cli.Flag{}
}

func (c *catCommand) Action(ctx context.Context, cmd *cli.Command) error {

	db, err := openDB()
	if err != nil {
		return err
	}

	id := cmd.Args().Get(0)
	err = validateShortID(id)
	if err != nil {
		return err
	}

	dotfile, err := getDotfileByID(db, id)
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stdout, string(dotfile.Data))
	return nil
}

func CatCommand() *catCommand {
	return &catCommand{
		name:  "cat",
		usage: "prints the dotfile to stdout",
	}
}
