package git

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

// Repo represents a local git repository.
type Repo struct {
	path string
}

// GetRepo returns a Repo for the given local path.
func GetRepo(p string) (*Repo, error) {
	if !isExist(p) {
		return nil, fmt.Errorf("repo does not exist: '%s'", p)
	}
	if !isExist(filepath.Join(p, ".git")) {
		return nil, fmt.Errorf("not a git repository: '%s'", p)
	}
	return &Repo{path: p}, nil
}

// Clone clones a remote repository using the given arguments.
func Clone(ctx context.Context, args ...string) error {
	r := Repo{}
	return r.gitExec(ctx, io.Discard, os.Stderr, append([]string{"clone"}, args...)...)
}

// Pull fetches and merges remote changes.
func (r *Repo) Pull(ctx context.Context) error {
	return r.gitExec(ctx, io.Discard, os.Stderr, "-C", r.path, "pull")
}

// Rebase pulls with rebase.
func (r *Repo) Rebase(ctx context.Context) error {
	return r.gitExec(ctx, io.Discard, os.Stderr, "-C", r.path, "pull", "--rebase")
}

// Add stages the given paths.
func (r *Repo) Add(ctx context.Context, args ...string) error {
	return r.gitExec(ctx, io.Discard, os.Stderr, append([]string{"-C", r.path, "add"}, args...)...)
}

// Remove stages the deletion of the given paths.
func (r *Repo) Remove(ctx context.Context, args ...string) error {
	if len(args) == 0 {
		return nil
	}
	return r.gitExec(ctx, io.Discard, os.Stderr, append([]string{"-C", r.path, "rm", "--quiet", "--"}, args...)...)
}

// Commit creates a commit with the given message.
func (r *Repo) Commit(ctx context.Context, message string) error {
	return r.gitExec(ctx, io.Discard, os.Stderr, "-C", r.path, "commit", "-m", message)
}

// Push pushes to the remote, rebasing and retrying once on rejection.
func (r *Repo) Push(ctx context.Context) error {
	var buf bytes.Buffer
	err := r.gitExec(ctx, io.Discard, io.MultiWriter(os.Stderr, &buf), "-C", r.path, "push")
	if err == nil {
		return nil
	}
	if !isRejectedPush(buf.String()) {
		return err
	}

	fmt.Fprintln(os.Stderr, "push rejected: pulling with rebase and retrying...")
	if rebaseErr := r.Rebase(ctx); rebaseErr != nil {
		return fmt.Errorf("pull --rebase after rejected push: %w", rebaseErr)
	}
	return r.gitExec(ctx, io.Discard, os.Stderr, "-C", r.path, "push")
}

func isRejectedPush(stderr string) bool {
	return strings.Contains(stderr, "[rejected]") || strings.Contains(stderr, "fetch first")
}

func (r *Repo) gitExec(ctx context.Context, out, stderr io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec
	cmd.Stderr = stderr
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("exec command 'git' %v: %w", args, err)
	}
	return nil
}

func isExist(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
