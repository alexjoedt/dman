package main

import (
	"context"
	"fmt"
	"os"

	"github.com/alexjoedt/dman/internal/app"
	"github.com/urfave/cli/v3"
)

// Version is set at build time via ldflags.
var Version = "dev"

func main() {
	a, err := app.NewApp()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	root := newRootCommand(a)

	if err := root.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand(a *app.App) *cli.Command {
	return &cli.Command{
		Name:        "dman",
		Usage:       "a dotfile manager",
		Description: "a simple but powerful dotfile manager",
		Commands: []*cli.Command{
			{
				Name:      "init",
				Usage:     "initialize dman by cloning a dotfile repository",
				ArgsUsage: "<repo-url>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "destination", Aliases: []string{"d"}, Usage: "local path to clone into"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return a.Init(ctx, c.Args().First(), c.String("destination"))
				},
			},
			{
				Name:  "apply",
				Usage: "apply dotfiles from base and active profile to home directory",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "profile", Aliases: []string{"p"}, Usage: "profile to apply (overrides config)"},
					&cli.BoolFlag{Name: "dry-run", Usage: "show what would change without applying"},
					&cli.BoolFlag{Name: "no-pull", Usage: "skip git pull before applying"},
					&cli.BoolFlag{Name: "no-snapshot", Usage: "skip automatic snapshot before applying"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return a.Apply(ctx, c.String("profile"), c.Bool("dry-run"), c.Bool("no-pull"), c.Bool("no-snapshot"))
				},
			},
			{
				Name:      "add",
				Usage:     "add dotfiles to the repository",
				ArgsUsage: "<file> [<file>...]",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "profile", Aliases: []string{"p"}, Usage: "add to this profile instead of base"},
					&cli.BoolFlag{Name: "no-push", Usage: "commit without pushing to remote"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return a.Add(ctx, c.Args().Slice(), c.String("profile"), c.Bool("no-push"))
				},
			},
			{
				Name:  "pull",
				Usage: "pull changes from the remote",
				Action: func(ctx context.Context, c *cli.Command) error {
					return a.Pull(ctx)
				},
			},
			{
				Name:  "push",
				Usage: "push changes to the remote",
				Action: func(ctx context.Context, c *cli.Command) error {
					return a.Push(ctx)
				},
			},
			{
				Name:  "cd",
				Usage: "open a shell in the local repository path",
				Action: func(ctx context.Context, c *cli.Command) error {
					return a.Cd(ctx)
				},
			},
			{
				Name:  "purge",
				Usage: "remove all dman files",
				Action: func(ctx context.Context, c *cli.Command) error {
					return a.Purge(ctx)
				},
			},
			{
				Name:  "version",
				Usage: "print the version",
				Action: func(ctx context.Context, c *cli.Command) error {
					fmt.Println(Version)
					return nil
				},
			},
			{
				Name:  "snapshot",
				Usage: "manage dotfile snapshots",
				Commands: []*cli.Command{
					{
						Name:  "list",
						Usage: "list all snapshots",
						Action: func(ctx context.Context, c *cli.Command) error {
							return a.SnapshotList(ctx)
						},
					},
					{
						Name:      "create",
						Usage:     "create a snapshot of all tracked dotfiles",
						ArgsUsage: "[--message <text>]",
						Flags: []cli.Flag{
							&cli.StringFlag{Name: "message", Aliases: []string{"m"}, Usage: "optional snapshot message"},
						},
						Action: func(ctx context.Context, c *cli.Command) error {
							return a.SnapshotCreate(ctx, c.String("message"))
						},
					},
					{
						Name:      "show",
						Usage:     "show files in a snapshot",
						ArgsUsage: "<snapshot-id>",
						Action: func(ctx context.Context, c *cli.Command) error {
							if c.Args().Len() == 0 {
								return fmt.Errorf("snapshot-id required")
							}
							return a.SnapshotShow(ctx, c.Args().First())
						},
					},
					{
						Name:      "cat",
						Usage:     "print file content by checksum",
						ArgsUsage: "<checksum>",
						Action: func(ctx context.Context, c *cli.Command) error {
							if c.Args().Len() == 0 {
								return fmt.Errorf("checksum required")
							}
							return a.SnapshotCat(ctx, c.Args().First())
						},
					},
					{
						Name:      "delete",
						Usage:     "delete a snapshot",
						ArgsUsage: "<snapshot-id>",
						Action: func(ctx context.Context, c *cli.Command) error {
							if c.Args().Len() == 0 {
								return fmt.Errorf("snapshot-id required")
							}
							return a.SnapshotDelete(ctx, c.Args().First())
						},
					},
				},
			},
		},
	}
}
