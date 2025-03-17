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
		Add(dman.ApplyCommand())

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
