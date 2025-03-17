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
	if !isExist(ConfigFile()) {
		return ErrNoConfig
	}

	repo := RepoDir()

	if err := gitPull(ctx, repo); err != nil {
		return err
	}

	files2apply, err := getDotfiles(repo)
	if err != nil {
		return fmt.Errorf("get dot files: %w", err)
	}

	if a.dryRun {
		printFileTable(files2apply.ApplyDry())
		return nil
	}

	db, err := openDB()
	if err != nil {
		return err
	}

	err = createSnapshot(db, files2apply.GetFilesFromHome(), "before-apply")
	if err != nil {
		return err
	}

	return files2apply.Apply()
}

func getDotfiles(p string) (*DotfileMapping, error) {
	m := &DotfileMapping{mapping: make(map[string]string)}
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, fmt.Errorf("read dir '%s': %w", p, err)
	}

	for _, entry := range entries {
		if strings.Contains(entry.Name(), "dot_") {
			if err := update(p, entry, m); err != nil {
				continue
			}
		}
	}

	return m, nil
}

func update(p string, entry os.DirEntry, m *DotfileMapping) error {
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

	m.Set(src, dst)
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

func getFromRepo(m map[string]string) []string {
	home := make([]string, 0, len(m))
	for f := range m {
		home = append(home, f)
	}
	return home
}
