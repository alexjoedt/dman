package dman

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/urfave/cli/v3"
)

type initCommand struct {
	name  string
	usage string
	flags []cli.Flag

	branch string
}

func InitCommand() *initCommand {
	var (
		branch string
	)

	c := &initCommand{
		name:  "init",
		usage: "inits the dotfile repository",
		flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "branch",
				Aliases:     []string{"b"},
				Destination: &branch,
			},
		},
	}

	c.branch = branch

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
	dest := RepoDir()
	if isExist(dest) {
		return fmt.Errorf("repository already exists (%s)", dest)
	}

	address := c.Args().First()
	if address == "" {
		return errors.New("empty address for dotfile repository")
	}
	_, err := url.Parse(address)
	if err != nil {
		return fmt.Errorf("invalid address '%s': %w", address, err)
	}

	args := []string{address, dest}
	if icmd.branch != "" {
		args = []string{address, "--branch", icmd.branch, dest}
	}
	if err := gitClone(context.Background(), args...); err != nil {
		return fmt.Errorf("git clone '%s': %w", address, err)
	}

	b, err := gitGetCurrentBranch(ctx, dest)
	if err != nil {
		return fmt.Errorf("init repo: %w", err)
	}

	if err := saveConfig(&Config{Repository: address, Branch: b}); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	return nil
}
