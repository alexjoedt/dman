package dman

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v3"
)

type purgeCommand struct {
	name  string
	usage string
}

func PurgeCommand() *purgeCommand {
	return &purgeCommand{
		name:  "purge",
		usage: "remove all dman files",
	}
}

func (i *purgeCommand) Name() string {
	return i.name
}

func (i *purgeCommand) Usage() string {
	return i.usage
}

func (i *purgeCommand) Flags() []cli.Flag {
	return []cli.Flag{}
}

func (icmd *purgeCommand) Action(ctx context.Context, c *cli.Command) error {
	fmt.Print("Do you really want to purge all related files?? (y/N): ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))
	if input != "y" {
		fmt.Println("Purge aborted.")
		return nil
	}

	if err := os.RemoveAll(ConfigDir()); err != nil {
		return err
	}
	fmt.Printf("\nRemoved %s\n", ConfigDir())

	if err := os.RemoveAll(RepoDir()); err != nil {
		return err
	}
	fmt.Println("Removed", RepoDir())

	return nil

}
