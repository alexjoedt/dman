package dman

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type repo struct {
	path string
	out  io.Writer
}

func getRepo(p string) (*repo, error) {
	if !isExist(p) {
		return nil, fmt.Errorf("repo does not exist: '%s'", p)
	}
	return &repo{
		path: p,
		out:  io.Discard,
	}, nil
}

func (r *repo) Pull(ctx context.Context) error {
	if err := r.gitExec(ctx, "-C", r.path, "pull"); err != nil {
		return fmt.Errorf("pull: %w", err)
	}
	return nil
}

func (r *repo) Clone(ctx context.Context, args ...string) error {
	return r.gitExec(ctx, append([]string{"clone"}, args...)...)
}

func cloneRepo(ctx context.Context, args ...string) error {
	r := repo{}
	return r.gitExec(ctx, append([]string{"clone"}, args...)...)
}

func (r *repo) Add(ctx context.Context, args ...string) error {
	return r.gitExec(ctx, append([]string{"-C", r.path, "add"}, args...)...)
}

func (r *repo) Commit(ctx context.Context, message string) error {
	return r.gitExec(ctx, "-C", r.path, "commit", "-m", message)
}

func (r *repo) Push(ctx context.Context) error {
	return r.gitExec(ctx, "-C", r.path, "push")
}

func (r *repo) CurrentBranch(ctx context.Context) (string, error) {
	b := &bytes.Buffer{}
	defer func() {
		b.Reset()
		r.out = io.Discard
	}()

	r.out = b
	err := r.gitExec(ctx, "-C", r.path, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("branch: %w", err)
	}

	return strings.Trim(b.String(), "\n"), nil
}

func (r *repo) gitExec(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = r.out

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("exec command 'git' %v: %w", args, err)
	}
	return nil
}
