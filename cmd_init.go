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

	branch string
	dest   string
}

func InitCommand() *initCommand {
	return &initCommand{
		name:  "init",
		usage: "inits the dotfile repository",
	}
}

func (i *initCommand) Name() string {
	return i.name
}

func (i *initCommand) Usage() string {
	return i.usage
}

func (i *initCommand) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "branch",
			Aliases:     []string{"b"},
			Destination: &i.branch,
		},
		&cli.StringFlag{
			Name:        "destination",
			Aliases:     []string{"d"},
			Destination: &i.dest,
		},
	}
}

func (icmd *initCommand) Action(ctx context.Context, c *cli.Command) error {
	dest := RepoDir()
	if icmd.dest != "" {
		dest = icmd.dest
	}

	if isExist(dest) {
		return fmt.Errorf("repository already exists (%s)", dest)
	}

	if isExist(DatabasePath()) {
		return fmt.Errorf("dman is already initialized")
	}

	db, err := openDB()
	if err != nil {
		return fmt.Errorf("init db: %w", err)
	}
	defer db.Close()

	address := c.Args().First()
	if address == "" {
		return errors.New("empty address for dotfile repository")
	}
	_, err = url.Parse(address)
	if err != nil {
		return fmt.Errorf("invalid address '%s': %w", address, err)
	}

	args := []string{address, dest}
	if icmd.branch != "" {
		args = []string{address, "--branch", icmd.branch, dest}
	}
	if err := cloneRepo(context.Background(), args...); err != nil {
		return fmt.Errorf("git clone '%s': %w", address, err)
	}

	repo, err := getRepo(dest)
	if err != nil {
		return err
	}

	b, err := repo.CurrentBranch(ctx)
	if err != nil {
		return fmt.Errorf("init repo: %w", err)
	}

	if err := saveConfig(&Config{Repository: address, Branch: b, Path: dest}); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	return nil
}
