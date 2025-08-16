package dman

import (
	"context"

	"github.com/urfave/cli/v3"
)

type pullCommand struct {
	name  string
	usage string

	// flags
}

func (a *pullCommand) Name() string {
	return a.name
}

func (a *pullCommand) Usage() string {
	return a.usage
}

func (a *pullCommand) Flags() []cli.Flag {
	return []cli.Flag{}
}

func (a *pullCommand) Action(ctx context.Context, c *cli.Command) error {
	repo, err := getRepo(RepoDir())
	if err != nil {
		return err
	}

	return repo.Pull(ctx)
}

func PullCommand() *pullCommand {
	return &pullCommand{
		name:  "pull",
		usage: "pulls changes from the remote",
	}
}
