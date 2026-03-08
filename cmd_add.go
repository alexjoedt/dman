package dman

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
)

type addCommand struct {
	name  string
	usage string

	// flags
}

func (a *addCommand) Name() string {
	return a.name
}

func (a *addCommand) Usage() string {
	return a.usage
}

func (a *addCommand) Flags() []cli.Flag {
	return []cli.Flag{}
}

func (a *addCommand) Action(ctx context.Context, c *cli.Command) error {
	report := make(map[string]string)
	for _, f := range c.Args().Slice() {
		if err := addFile(f, report); err != nil {
			return err
		}
	}

	repo, err := getRepo(RepoDir())
	if err != nil {
		return err
	}

	for k := range report {
		fmt.Printf("%s: %s\n", report[k], k)
		file, err := transformPath(HomeDir().Path, RepoDir(), k)
		if err != nil {
			return err
		}

		err = repo.Add(ctx, file)
		if err != nil {
			return err
		}

		err = repo.Commit(ctx, report[k]+" "+file)
		if err != nil {
			return err
		}
	}

	return repo.Push(ctx)
}

func AddCommand() *addCommand {
	return &addCommand{
		name:  "add",
		usage: "adds a dotfile(s) to the repository",
	}
}

// addFile adds a dotfile from the home dir to the repository
func addFile(src string, report map[string]string) error {
	dst, err := transformPath(HomeDir().Path, RepoDir(), src)
	if err != nil {
		return fmt.Errorf("add file: %w", err)
	}

	if isExist(dst) {
		report[src] = "update"
	} else {
		report[src] = "add"
	}

	if err := copyFile(dst, src); err != nil {
		return fmt.Errorf("add file: %w", err)
	}

	return nil
}
