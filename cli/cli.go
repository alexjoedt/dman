package cli

import (
	"context"

	"github.com/urfave/cli/v3"
)

type Command struct {
	cmd cli.Command
}

func (c *Command) Add(ac *cli.Command) *Command {
	c.cmd.Commands = append(c.cmd.Commands, ac)
	return c
}

func (c *Command) Run(ctx context.Context, osArgs []string) error {
	return c.cmd.Run(ctx, osArgs)
}
