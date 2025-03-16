package main

import (
	"context"
	"log"
	"os"

	"github.com/alexjoedt/dman"
	"github.com/alexjoedt/dman/cli"
)

func main() {
	cmd := cli.Command{}

	cmd.
		Add(dman.InitCommand())

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
