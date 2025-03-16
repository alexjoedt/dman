package main

import (
	"context"
	"log"
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
		Add(dman.InitCommand())

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
