package main

import (
	"context"
	"fmt"
	"os"

	"github.com/alexjoedt/dman"
	"github.com/urfave/cli/v3"
)

func main() {
	app, err := dman.NewApp()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	root := &cli.Command{
		Name:        "dman",
		Usage:       "a dotfile manager",
		Description: "a simple but powerful dotfile manager",
		Commands: []*cli.Command{
			{
				Name:  "init",
				Usage: "inits the dotfile repository",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "branch", Aliases: []string{"b"}},
					&cli.StringFlag{Name: "destination", Aliases: []string{"d"}},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return app.Init(ctx, c.Args().First(), c.String("branch"), c.String("destination"))
				},
			},
			{
				Name:  "apply",
				Usage: "applies all dotfiles from the repository",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "dry-run"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return app.Apply(ctx, c.Bool("dry-run"))
				},
			},
			{
				Name:  "add",
				Usage: "adds dotfiles to the repository",
				Action: func(ctx context.Context, c *cli.Command) error {
					return app.Add(ctx, c.Args().Slice())
				},
			},
			{
				Name:  "pull",
				Usage: "pulls changes from the remote",
				Action: func(ctx context.Context, c *cli.Command) error {
					return app.Pull(ctx)
				},
			},
			{
				Name:  "backup",
				Usage: "creates a snapshot of the current dotfiles in the home directory",
				Flags: []cli.Flag{
					&cli.StringSliceFlag{Name: "tag", Usage: "add tags to the backup"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return app.Backup(ctx, c.StringSlice("tag"))
				},
			},
			{
				Name:  "snapshots",
				Usage: "work with snapshots",
				Action: func(ctx context.Context, c *cli.Command) error {
					return app.Snapshots(ctx)
				},
			},
			{
				Name:  "list",
				Usage: "list dotfiles",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "all", Aliases: []string{"a"}},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return app.List(ctx, c.Args().Get(0), c.Bool("all"))
				},
			},
			{
				Name:  "cat",
				Usage: "prints the dotfile to stdout",
				Action: func(ctx context.Context, c *cli.Command) error {
					return app.Cat(ctx, c.Args().Get(0))
				},
			},
			{
				Name:  "restore",
				Usage: "restore dotfiles from a specific snapshot",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "dry-run", Usage: "show what would be restored without making changes"},
					&cli.StringFlag{Name: "file", Aliases: []string{"f"}, Usage: "restore only the specified dotfile"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return app.Restore(ctx, c.Args().Get(0), c.String("file"), c.Bool("dry-run"))
				},
			},
			{
				Name:  "purge",
				Usage: "remove all dman files",
				Action: func(ctx context.Context, c *cli.Command) error {
					return app.Purge(ctx)
				},
			},
			{
				Name:  "env",
				Usage: "manage environments (git branches)",
				Commands: []*cli.Command{
					{
						Name:  "list",
						Usage: "list all environments",
						Action: func(ctx context.Context, c *cli.Command) error {
							return app.EnvList(ctx)
						},
					},
					{
						Name:      "switch",
						Usage:     "switch to an environment",
						ArgsUsage: "<name>",
						Action: func(ctx context.Context, c *cli.Command) error {
							if c.Args().Len() == 0 {
								return fmt.Errorf("switch requires an environment name")
							}
							return app.EnvSwitch(ctx, c.Args().First())
						},
					},
					{
						Name:      "create",
						Usage:     "create a new environment",
						ArgsUsage: "<name>",
						Action: func(ctx context.Context, c *cli.Command) error {
							if c.Args().Len() == 0 {
								return fmt.Errorf("create requires an environment name")
							}
							return app.EnvCreate(ctx, c.Args().First())
						},
					},
					{
						Name:  "current",
						Usage: "show the current environment",
						Action: func(ctx context.Context, c *cli.Command) error {
							return app.EnvCurrent(ctx)
						},
					},
				},
			},
		},
	}

	if err := root.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
