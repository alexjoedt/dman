package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alexjoedt/log"
)

// Repo represents a local git repository.
type Repo struct {
	path   string
	Stderr io.Writer // destination for git subprocess stderr; defaults to io.Discard
}

// GetRepo returns a Repo for the given local path.
func GetRepo(p string) (*Repo, error) {
	if !isExist(p) {
		return nil, fmt.Errorf("repo does not exist: '%s'", p)
	}
	if !isExist(filepath.Join(p, ".git")) {
		return nil, fmt.Errorf("not a git repository: '%s'", p)
	}
	return &Repo{path: p, Stderr: io.Discard}, nil
}

// Clone clones a remote repository using the given arguments.
func Clone(ctx context.Context, stderr io.Writer, args ...string) error {
	r := Repo{Stderr: stderr}
	return r.gitExec(ctx, io.Discard, stderr, append([]string{"clone"}, args...)...)
}

// Pull fetches and merges remote changes.
func (r *Repo) Pull(ctx context.Context) error {
	return r.gitExec(ctx, io.Discard, r.Stderr, "-C", r.path, "pull")
}

// Rebase pulls with rebase.
func (r *Repo) Rebase(ctx context.Context) error {
	return r.gitExec(ctx, io.Discard, r.Stderr, "-C", r.path, "pull", "--rebase")
}

// Add stages the given paths.
func (r *Repo) Add(ctx context.Context, args ...string) error {
	return r.gitExec(ctx, io.Discard, r.Stderr, append([]string{"-C", r.path, "add"}, args...)...)
}

// Remove stages the deletion of the given paths. Paths that were never
// tracked are ignored, so callers may pass files they deleted from disk
// before staging was ever enabled.
func (r *Repo) Remove(ctx context.Context, args ...string) error {
	if len(args) == 0 {
		return nil
	}
	return r.gitExec(ctx, io.Discard, r.Stderr, append([]string{"-C", r.path, "rm", "--quiet", "--ignore-unmatch", "--"}, args...)...)
}

// Commit creates a commit with the given message. With paths, only those
// paths are committed and anything else already staged stays staged.
func (r *Repo) Commit(ctx context.Context, message string, paths ...string) error {
	args := []string{"-C", r.path, "commit", "-m", message}
	if len(paths) > 0 {
		args = append(append(args, "--"), paths...)
	}
	return r.gitExec(ctx, io.Discard, r.Stderr, args...)
}

// Push pushes to the remote, rebasing and retrying once on rejection.
func (r *Repo) Push(ctx context.Context) error {
	var buf bytes.Buffer
	err := r.gitExec(ctx, io.Discard, io.MultiWriter(r.Stderr, &buf), "-C", r.path, "push")
	if err == nil {
		return nil
	}
	if !isRejectedPush(buf.String()) {
		return err
	}

	log.Warn("push rejected: pulling with rebase and retrying...")
	if rebaseErr := r.Rebase(ctx); rebaseErr != nil {
		inProgress, checkErr := r.rebaseInProgress(context.WithoutCancel(ctx))
		if checkErr != nil {
			return errors.Join(
				fmt.Errorf("push not done, pull --rebase after rejected push failed: %w", rebaseErr),
				fmt.Errorf("cannot tell whether repository %s is mid-rebase: %w", r.path, checkErr),
			)
		}
		if !inProgress {
			return fmt.Errorf("push not done, pull --rebase after rejected push failed: %w", rebaseErr)
		}
		abort := r.gitExec(context.WithoutCancel(ctx), io.Discard, r.Stderr, "-C", r.path, "rebase", "--abort")
		if abort != nil {
			return errors.Join(
				fmt.Errorf("push not done, pull --rebase after rejected push failed: %w", rebaseErr),
				fmt.Errorf("rebase --abort failed, repository %s is mid-rebase: %w", r.path, abort),
			)
		}
		return fmt.Errorf("push not done, pull --rebase after rejected push failed and the rebase was aborted: %w", rebaseErr)
	}
	return r.gitExec(ctx, io.Discard, r.Stderr, "-C", r.path, "push")
}

// RemoteURL returns the URL of the remote that pull uses, without talking to
// it. A repository without a remote yields an empty URL.
func (r *Repo) RemoteURL(ctx context.Context) (string, error) {
	var out bytes.Buffer
	if err := r.gitExec(ctx, &out, r.Stderr, "-C", r.path, "ls-remote", "--get-url"); err != nil {
		var remotes bytes.Buffer
		if r.gitExec(ctx, &remotes, r.Stderr, "-C", r.path, "remote") == nil && strings.TrimSpace(remotes.String()) == "" {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

func (r *Repo) rebaseInProgress(ctx context.Context) (bool, error) {
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		var out bytes.Buffer
		if err := r.gitExec(ctx, &out, io.Discard, "-C", r.path, "rev-parse", "--git-path", name); err != nil {
			return false, err
		}
		p := strings.TrimSpace(out.String())
		if !filepath.IsAbs(p) {
			p = filepath.Join(r.path, p)
		}
		if isExist(p) {
			return true, nil
		}
	}
	return false, nil
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
