package dman

import (
	"context"
	"fmt"
	"net/url"

	"github.com/urfave/cli/v3"
)

func InitCommand() *cli.Command {

	c := &cli.Command{
		Name:   "init",
		Usage:  "inits the dotfile repository",
		Action: initDotfilesRepository,
	}
	return nil
}

func initDotfilesRepository(ctx context.Context, c *cli.Command) error {
	address := c.Args().First()
	_, err := url.Parse(address)
	if err != nil {
		return fmt.Errorf("invalid address '%s': %w", address, err)
	}

	return nil
}
