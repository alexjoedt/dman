package cli

import (
	"context"

	"github.com/urfave/cli/v3"
)

type Cli struct {
	root *cli.Command
}

type Config struct {
	Name        string
	Usage       string
	Description string
}

func New(config *Config) *Cli {
	return &Cli{
		root: &cli.Command{
			Name:        config.Name,
			Description: config.Description,
			Usage:       config.Usage,
		},
	}
}

type Command interface {
	Name() string
	Usage() string
	Flags() []cli.Flag
	Action(context.Context, *cli.Command) error
}

func (c *Cli) Add(ac Command) *Cli {

	cmd := &cli.Command{
		Name:   ac.Name(),
		Usage:  ac.Usage(),
		Flags:  ac.Flags(),
		Action: ac.Action,
	}

	c.root.Commands = append(c.root.Commands, cmd)
	return c
}

func (c *Cli) Run(ctx context.Context, osArgs []string) error {
	return c.root.Run(ctx, osArgs)
}
