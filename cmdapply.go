package dman

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
)

type applyCommand struct {
	name  string
	usage string
	flags []cli.Flag

	exclude []string
}

func ApplyCommand() *applyCommand {
	var (
		exclude []string
	)
	acmd := &applyCommand{
		name:  "apply",
		usage: "applies all dotfiles from the repository",
		flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:        "exclude",
				Destination: &exclude,
			},
		},
	}
	acmd.exclude = exclude
	return acmd
}

func (a *applyCommand) Name() string {
	return a.name
}

func (a *applyCommand) Usage() string {
	return a.usage
}

func (a *applyCommand) Action(ctx context.Context, cmd *cli.Command) error {

	files2apply := make(map[string]string)
	err := apply(RepoDir(), files2apply)
	if err != nil {
		return fmt.Errorf("get dot files: %w", err)
	}

	return nil
}

func apply(basePath string, m map[string]string) error {
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if strings.Contains(entry.Name(), "dot_") {
			if err := update(basePath, entry, m); err != nil {
				continue
			}
		}
	}

	return nil
}

func update(p string, entry os.DirEntry, m map[string]string) error {
	if entry.IsDir() {
		entries, err := os.ReadDir(filepath.Join(p, entry.Name()))
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := update(filepath.Join(p, entry.Name()), e, m); err != nil {
				return err
			}
		}
		return nil
	}

	src := filepath.Join(p, entry.Name())
	dst := strings.ReplaceAll(src, RepoDir(), HomeDir().Name())
	dst = strings.ReplaceAll(dst, "dot_", ".")

	m[src] = dst
	return nil
}
