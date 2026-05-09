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
}

func getRepo(p string) (*repo, error) {
	if !isExist(p) {
		return nil, fmt.Errorf("repo does not exist: '%s'", p)
	}
	return &repo{path: p}, nil
}

func (r *repo) Pull(ctx context.Context) error {
	if err := r.gitExec(ctx, io.Discard, "-C", r.path, "pull"); err != nil {
		return fmt.Errorf("pull: %w", err)
	}
	return nil
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

func (r *repo) CurrentBranch(ctx context.Context) (string, error) {
	var b bytes.Buffer
	if err := r.gitExec(ctx, &b, "-C", r.path, "rev-parse", "--abbrev-ref", "HEAD"); err != nil {
		return "", fmt.Errorf("branch: %w", err)
	}
	return strings.Trim(b.String(), "\n"), nil
}

func (r *repo) ListBranches(ctx context.Context) ([]string, string, error) {
	var b bytes.Buffer
	if err := r.gitExec(ctx, &b, "-C", r.path, "branch", "-a"); err != nil {
		return nil, "", fmt.Errorf("list branches: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	var branches []string
	var current string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "* ") {
			// Current branch
			current = strings.TrimPrefix(line, "* ")
			if current != "HEAD" {
				branches = append(branches, current)
			}
		} else if strings.HasPrefix(line, "remotes/origin/") {
			// Remote branch
			branch := strings.TrimPrefix(line, "remotes/origin/")
			if strings.Contains(branch, "HEAD") {
				continue
			}
			// Only add if not already in branches (avoid duplicates)
			found := false
			for _, existing := range branches {
				if existing == branch {
					found = true
					break
				}
			}
			if !found {
				branches = append(branches, branch)
			}
		}
	}

	return branches, current, nil
}

func (r *repo) Checkout(ctx context.Context, branch string) error {
	return r.gitExec(ctx, io.Discard, "-C", r.path, "checkout", branch)
}

func (r *repo) CheckoutNewBranch(ctx context.Context, branch string) error {
	return r.gitExec(ctx, io.Discard, "-C", r.path, "checkout", "-b", branch)
}

func (r *repo) PushNewBranch(ctx context.Context, branch string) error {
	return r.gitExec(ctx, io.Discard, "-C", r.path, "push", "-u", "origin", branch)
}

func (r *repo) gitExec(ctx context.Context, out io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = out

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("exec command 'git' %v: %w", args, err)
	}
	return nil
}
