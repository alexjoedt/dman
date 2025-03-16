package dman

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

func gitClone(u string, dest string) error {

	return nil
}

func gitPull(dest string) error {

	return nil
}

func gitExec(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("exec command 'git %v: %w", args, err)
	}
	return nil
}
