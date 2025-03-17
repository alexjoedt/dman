package dman

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type repo struct {
	path string
}

func (r *repo) Pull(ctx context.Context) error {
	return gitPull(ctx, r.path)
}

func gitClone(ctx context.Context, args ...string) error {
	return gitExec(ctx, append([]string{"clone"}, args...)...)
}

func gitPull(ctx context.Context, dest string) error {
	if err := gitExec(ctx, "-C", dest, "pull"); err != nil {
		return fmt.Errorf("pull: %w", err)
	}
	return nil
}

func gitAdd(ctx context.Context, dest string, f string) error {
	return gitExec(ctx, "-C", dest, "add", f)
}

func gitCommit(ctx context.Context, dest string, message string) error {
	return gitExec(ctx, "-C", dest, "commit", "-q", "-m", message)
}

func gitPush(ctx context.Context, dest string) error {
	return gitExec(ctx, "-C", dest, "push", "-q")
}

func gitGetCurrentBranch(ctx context.Context, dest string) (string, error) {
	b, err := gitExecOut(ctx, "-C", dest, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("branch: %w", err)
	}

	return strings.Trim(b, "\n"), nil
}

func gitExec(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("exec command 'git' %v: %w", args, err)
	}
	return nil
}

func gitExecOut(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("exec command 'git' with output %v: %w", args, err)
	}

	return string(out), nil
}
