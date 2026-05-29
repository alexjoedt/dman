package dman

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

type repo struct {
	path string
}

func getRepo(p string) (*repo, error) {
	if !isExist(p) {
		return nil, fmt.Errorf("repo does not exist: '%s'", p)
	}
	if !isExist(filepath.Join(p, ".git")) {
		return nil, fmt.Errorf("not a git repository: '%s'", p)
	}
	return &repo{path: p}, nil
}

func (r *repo) Pull(ctx context.Context) error {
	return r.gitExec(ctx, io.Discard, "-C", r.path, "pull")
}

func cloneRepo(ctx context.Context, args ...string) error {
	r := repo{}
	return r.gitExec(ctx, io.Discard, append([]string{"clone"}, args...)...)
}

func (r *repo) Add(ctx context.Context, args ...string) error {
	return r.gitExec(ctx, io.Discard, append([]string{"-C", r.path, "add"}, args...)...)
}

func (r *repo) Commit(ctx context.Context, message string) error {
	return r.gitExec(ctx, io.Discard, "-C", r.path, "commit", "-m", message)
}

func (r *repo) Push(ctx context.Context) error {
	return r.gitExec(ctx, io.Discard, "-C", r.path, "push")
}

func (r *repo) gitExec(ctx context.Context, out io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // git with controlled args
	cmd.Stderr = os.Stderr
	cmd.Stdout = out

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("exec command 'git' %v: %w", args, err)
	}
	return nil
}
