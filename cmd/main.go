package main

import (
	"context"
	"fmt"
	"os"

	"github.com/alexjoedt/dman"
	"github.com/urfave/cli/v3"
)

// Version is set at build time via ldflags.
var Version = "dev"

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
				Name:      "init",
				Usage:     "initialize dman by cloning a dotfile repository",
				ArgsUsage: "<repo-url>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "destination", Aliases: []string{"d"}, Usage: "local path to clone into"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return app.Init(ctx, c.Args().First(), c.String("destination"))
				},
			},
			{
				Name:  "apply",
				Usage: "apply dotfiles from base and active profile to home directory",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "profile", Aliases: []string{"p"}, Usage: "profile to apply (overrides config)"},
					&cli.BoolFlag{Name: "dry-run", Usage: "show what would change without applying"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return app.Apply(ctx, c.String("profile"), c.Bool("dry-run"))
				},
			},
			{
				Name:      "add",
				Usage:     "add dotfiles to the repository",
				ArgsUsage: "<file> [<file>...]",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "profile", Aliases: []string{"p"}, Usage: "add to this profile instead of base"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return app.Add(ctx, c.Args().Slice(), c.String("profile"))
				},
			},
			{
				Name:  "pull",
				Usage: "pull changes from the remote",
				Action: func(ctx context.Context, c *cli.Command) error {
					return app.Pull(ctx)
				},
			},
			{
				Name:  "push",
				Usage: "push changes to the remote",
				Action: func(ctx context.Context, c *cli.Command) error {
					return app.Push(ctx)
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
				Name:  "version",
				Usage: "print the version",
				Action: func(ctx context.Context, c *cli.Command) error {
					fmt.Println(Version)
					return nil
				},
			},
		},
	}

	if err := root.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
