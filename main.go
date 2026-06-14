package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/alexjoedt/dman/internal/app"
	"github.com/alexjoedt/log"
	"github.com/urfave/cli/v3"
)

// Version is set at build time via ldflags.
var Version = "dev"

// logLevel controls the global log level and can be changed at runtime.
var logLevel = &slog.LevelVar{} // defaults to INFO

func main() {
	logger := log.NewCLILogger(log.WithLevel(logLevel))
	log.SetDefault(logger)

	a, err := app.NewApp()
	if err != nil {
		log.Failure(err.Error())
		os.Exit(1)
	}

	root := newRootCommand(a)

	if err := root.Run(context.Background(), os.Args); err != nil {
		log.Failure(err.Error())
		os.Exit(1)
	}
}

func newRootCommand(a *app.App) *cli.Command {
	return &cli.Command{
		Name:        "dman",
		Usage:       "a dotfile manager",
		Description: "a simple but powerful dotfile manager",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"v"},
				Usage:   "enable verbose output (shows git commands and debug info)",
			},
		},
		Before: func(ctx context.Context, c *cli.Command) (context.Context, error) {
			if c.Bool("verbose") {
				logLevel.Set(slog.LevelDebug)
			}
			return ctx, nil
		},
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
				Name:      "apply",
				Usage:     "apply dotfiles from base and active profile to home directory",
				ArgsUsage: "[file...]",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "profile", Aliases: []string{"p"}, Usage: "profile to apply (overrides config)"},
					&cli.BoolFlag{Name: "dry-run", Usage: "show what would change without applying"},
					&cli.BoolFlag{Name: "no-pull", Usage: "skip git pull before applying"},
					&cli.BoolFlag{Name: "no-snapshot", Usage: "skip automatic snapshot before applying"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return a.Apply(ctx, c.String("profile"), c.Bool("dry-run"), c.Bool("no-pull"), c.Bool("no-snapshot"), c.Args().Slice())
				},
			},
			{
				Name:      "diff",
				Usage:     "show a unified diff between home files and the dotfile repo",
				ArgsUsage: "[file...]",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "profile", Aliases: []string{"p"}, Usage: "profile to diff (overrides config)"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return a.Diff(ctx, c.String("profile"), c.Args().Slice())
				},
			},
			{
				Name:      "add",
				Usage:     "add dotfiles to the repository",
				ArgsUsage: "<file> [<file>...]",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "profile", Aliases: []string{"p"}, Usage: "add to this profile instead of base"},
					&cli.StringFlag{Name: "sync", Usage: "sync from this directory and prune removed files"},
					&cli.BoolFlag{Name: "dry-run", Usage: "show sync changes without writing, staging, or committing"},
					&cli.BoolFlag{Name: "add", Usage: "stage copied files in git"},
					&cli.BoolFlag{Name: "commit", Usage: "create a commit for staged changes"},
					&cli.BoolFlag{Name: "push", Usage: "push committed changes to remote"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					syncDir := c.String("sync")
					if syncDir != "" {
						if c.Args().Len() > 0 {
							return fmt.Errorf("cannot pass file arguments with --sync")
						}
						return a.AddSync(ctx, syncDir, c.String("profile"), c.Bool("dry-run"), c.Bool("add"), c.Bool("commit"), c.Bool("push"))
					}
					if c.Bool("dry-run") {
						return fmt.Errorf("--dry-run is only supported with --sync")
					}
					return a.Add(ctx, c.Args().Slice(), c.String("profile"), c.Bool("add"), c.Bool("commit"), c.Bool("push"))
				},
			},
			{
				Name:  "sync",
				Usage: "update the repository from home for all tracked dotfiles (home -> repo)",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "profile", Aliases: []string{"p"}, Usage: "profile to sync (overrides config)"},
					&cli.BoolFlag{Name: "dry-run", Usage: "show what would change without writing, staging, or committing"},
					&cli.BoolFlag{Name: "add", Usage: "stage updated files in git"},
					&cli.BoolFlag{Name: "commit", Usage: "create a commit for staged changes"},
					&cli.BoolFlag{Name: "push", Usage: "push committed changes to remote"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return a.Sync(ctx, c.String("profile"), c.Bool("dry-run"), c.Bool("add"), c.Bool("commit"), c.Bool("push"))
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
				Name:  "profiles",
				Usage: "list and manage profiles from the dotfile repository",
				Commands: []*cli.Command{
					{
						Name:  "list",
						Usage: "list available profiles (active profile marked with *)",
						Action: func(ctx context.Context, c *cli.Command) error {
							return a.ConfigListProfiles(ctx)
						},
					},
					{
						Name:      "set",
						Usage:     "set the active profile",
						ArgsUsage: "<name>",
						Action: func(ctx context.Context, c *cli.Command) error {
							if c.Args().Len() == 0 {
								return fmt.Errorf("profile name required")
							}
							return a.ProfileSet(ctx, c.Args().First())
						},
					},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return a.ConfigListProfiles(ctx)
				},
			},
			{
				Name:      "config",
				Usage:     "read and write config settings",
				ArgsUsage: "[<key> [<value>]]",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "unset", Usage: "reset the key to its zero value"},
				},
				Commands: []*cli.Command{
					{
						Name:      "list",
						Usage:     "list config settings or available profiles",
						ArgsUsage: "[profiles]",
						Action: func(ctx context.Context, c *cli.Command) error {
							if c.Args().First() == "profiles" {
								return a.ConfigListProfiles(ctx)
							}
							return a.ConfigShow(ctx)
						},
					},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					args := c.Args()
					unset := c.Bool("unset")
					switch {
					case args.Len() == 0:
						return a.ConfigShow(ctx)
					case args.Len() == 1 && unset:
						return a.ConfigUnset(ctx, args.First())
					case args.Len() == 1:
						return a.ConfigGet(ctx, args.First())
					case args.Len() == 2 && !unset:
						return a.ConfigSet(ctx, args.Get(0), args.Get(1))
					case args.Len() == 2 && unset:
						return fmt.Errorf("--unset does not take a value argument")
					default:
						return fmt.Errorf("usage: dman config [--unset] <key> [<value>]")
					}
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
