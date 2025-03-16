package dman

import (
	"context"
	"fmt"
	"net/url"

	"github.com/urfave/cli/v3"
)

type initCommand struct {
	name  string
	usage string
	flags []cli.Flag
}

func InitCommand() *initCommand {

	c := &initCommand{
		name:  "init",
		usage: "inits the dotfile repository",
	}

	return c
}

func (i *initCommand) Name() string {
	return i.name
}

func (i *initCommand) Usage() string {
	return i.usage
}

func (i *initCommand) Flags() []cli.Flag {
	return i.flags
}

func (icmd *initCommand) Action(ctx context.Context, c *cli.Command) error {
	address := c.Args().First()
	_, err := url.Parse(address)
	if err != nil {
		return fmt.Errorf("invalid address '%s': %w", address, err)
	}

	return nil
}
