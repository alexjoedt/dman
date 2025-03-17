package dman

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/urfave/cli/v3"
)

type applyCommand struct {
	name  string
	usage string

	exclude []string
	include []string
	dryRun  bool
}

func ApplyCommand() *applyCommand {
	return &applyCommand{
		name:  "apply",
		usage: "applies all dotfiles from the repository",
	}
}

func (a *applyCommand) Name() string {
	return a.name
}

func (a *applyCommand) Usage() string {
	return a.usage
}

func (a *applyCommand) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringSliceFlag{
			Name:        "exclude",
			Usage:       "exclude files",
			Destination: &a.exclude,
		},
		&cli.StringSliceFlag{
			Name:        "include",
			Destination: &a.include,
		},
		&cli.BoolFlag{
			Name:        "dry-run",
			Destination: &a.dryRun,
		},
	}
}

func (a *applyCommand) Action(ctx context.Context, cmd *cli.Command) error {
	config, err := readConfig()
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	files2apply := make(map[string]string)
	err = apply(config.Path, files2apply)
	if err != nil {
		return fmt.Errorf("get dot files: %w", err)
	}

	if a.dryRun {
		printFileTable(files2apply)
		return nil
	}

	return nil
}

func apply(p string, m map[string]string) error {
	entries, err := os.ReadDir(p)
	if err != nil {
		return fmt.Errorf("read dir '%s': %w", p, err)
	}

	for _, entry := range entries {
		if strings.Contains(entry.Name(), "dot_") {
			if err := update(p, entry, m); err != nil {
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

func printFileTable(m map[string]string) {
	w := tabwriter.NewWriter(os.Stdout, 10, 0, 2, ' ', 0)
	fmt.Fprintf(w, "DOTFILES\tHOME\n")
	fmt.Fprintf(w, "--------\t----\n")

	for d, h := range m {
		fmt.Fprintf(w, "%s\t%s\n", d, h)
	}

	w.Flush()
}
