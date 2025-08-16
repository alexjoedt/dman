package main

import (
	"context"
	"fmt"
	"os"

	"github.com/alexjoedt/dman"
	"github.com/alexjoedt/dman/cli"
)

func main() {
	cmd := cli.New(&cli.Config{
		Name:        "dman",
		Usage:       "a dotfile manager",
		Description: "a simple but powerful dotfile manager",
	})

	cmd.
		Add(dman.InitCommand()).
		Add(dman.ApplyCommand()).
		Add(dman.EnvCommand()).
		Add(dman.SnapshotCommand()).
		Add(dman.ListCommand()).
		Add(dman.PurgeCommand()).
		Add(dman.BackupCommand()).
		Add(dman.AddCommand()).
		Add(dman.CatCommand()).
		Add(dman.RestoreCommand()).
		Add(dman.PullCommand())

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
