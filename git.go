package dman

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	return r.gitExec(ctx, io.Discard, os.Stderr, "-C", r.path, "pull")
}

func (r *repo) Rebase(ctx context.Context) error {
	return r.gitExec(ctx, io.Discard, os.Stderr, "-C", r.path, "pull", "--rebase")
}

func cloneRepo(ctx context.Context, args ...string) error {
	r := repo{}
	return r.gitExec(ctx, io.Discard, os.Stderr, append([]string{"clone"}, args...)...)
}

func (r *repo) Add(ctx context.Context, args ...string) error {
	return r.gitExec(ctx, io.Discard, os.Stderr, append([]string{"-C", r.path, "add"}, args...)...)
}

func (r *repo) Commit(ctx context.Context, message string) error {
	return r.gitExec(ctx, io.Discard, os.Stderr, "-C", r.path, "commit", "-m", message)
}

func (r *repo) Push(ctx context.Context) error {
	var buf bytes.Buffer
	err := r.gitExec(ctx, io.Discard, io.MultiWriter(os.Stderr, &buf), "-C", r.path, "push")
	if err == nil {
		return nil
	}
	if !isRejectedPush(buf.String()) {
		return err
	}

	// Remote has commits we don't have — rebase and retry once.
	fmt.Fprintln(os.Stderr, "push rejected: pulling with rebase and retrying...")
	if rebaseErr := r.Rebase(ctx); rebaseErr != nil {
		return fmt.Errorf("pull --rebase after rejected push: %w", rebaseErr)
	}
	return r.gitExec(ctx, io.Discard, os.Stderr, "-C", r.path, "push")
}

func isRejectedPush(stderr string) bool {
	return strings.Contains(stderr, "[rejected]") || strings.Contains(stderr, "fetch first")
}

func (r *repo) gitExec(ctx context.Context, out io.Writer, stderr io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // git with controlled args
	cmd.Stderr = stderr
	cmd.Stdout = out

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("exec command 'git' %v: %w", args, err)
	}
	return nil
}
