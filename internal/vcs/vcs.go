package vcs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// LogEntry describes one git commit in history output.
type LogEntry struct {
	SHA     string
	Date    string
	Subject string
}

// EnsureRepo creates dir if needed and initializes it as a local git repository.
func EnsureRepo(ctx context.Context, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := gitOutput(ctx, dir, "init", "-q"); err != nil {
		return err
	}
	if _, err := gitOutput(ctx, dir, "config", "user.email", "daimon@localhost"); err != nil {
		return err
	}
	if _, err := gitOutput(ctx, dir, "config", "user.name", "daimon"); err != nil {
		return err
	}
	return nil
}

// Commit stages selected paths, or all changes when paths is empty, and commits
// them when there is a staged diff.
func Commit(ctx context.Context, dir, message string, paths ...string) (sha string, committed bool, err error) {
	addArgs := []string{"add", "-A"}
	if len(paths) > 0 {
		addArgs = []string{"add", "--"}
		for _, path := range paths {
			clean, err := cleanRelPath(path)
			if err != nil {
				return "", false, err
			}
			addArgs = append(addArgs, clean)
		}
	}
	if _, err := gitOutput(ctx, dir, addArgs...); err != nil {
		return "", false, err
	}

	cmd := exec.CommandContext(ctx, "git", "-C", dir, "diff", "--cached", "--quiet")
	cmd.Env = gitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		return "", false, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		return "", false, gitError(err, stderr.String())
	}

	if _, err := gitOutput(ctx, dir, "commit", "-q", "-m", message); err != nil {
		return "", false, err
	}
	out, err := gitOutput(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(out)), true, nil
}

// Log returns recent commits, optionally restricted to relpath.
func Log(ctx context.Context, dir, relpath string, limit int) ([]LogEntry, error) {
	if !hasCommits(ctx, dir) {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	args := []string{"log", "--max-count=" + strconv.Itoa(limit), "--pretty=format:%H%x1f%cI%x1f%s"}
	if strings.TrimSpace(relpath) != "" {
		clean, err := cleanRelPath(relpath)
		if err != nil {
			return nil, err
		}
		args = append(args, "--", clean)
	}
	out, err := gitOutput(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return nil, nil
	}
	lines := strings.Split(text, "\n")
	commits := make([]LogEntry, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "\x1f", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("parse git log line: %q", line)
		}
		commits = append(commits, LogEntry{SHA: parts[0], Date: parts[1], Subject: parts[2]})
	}
	return commits, nil
}

// RevertFileToPrevious restores relpath to its previous file-history state and commits the revert.
func RevertFileToPrevious(ctx context.Context, dir, relpath string) error {
	clean, err := cleanRelPath(relpath)
	if err != nil {
		return err
	}
	if !hasCommits(ctx, dir) {
		return errors.New("no previous version")
	}

	out, err := gitOutput(ctx, dir, "log", "-n", "2", "--format=%H", "--", clean)
	if err != nil {
		return err
	}
	lines := strings.Split(string(out), "\n")
	shas := make([]string, 0, len(lines))
	for _, line := range lines {
		if sha := strings.TrimSpace(line); sha != "" {
			shas = append(shas, sha)
		}
	}
	switch len(shas) {
	case 0:
		return fmt.Errorf("no history for %q", clean)
	case 1:
		if _, err := gitOutput(ctx, dir, "rm", "-q", "--", clean); err != nil {
			return err
		}
	default:
		if _, err := gitOutput(ctx, dir, "checkout", shas[1], "--", clean); err != nil {
			return err
		}
	}
	_, err = gitOutput(ctx, dir, "commit", "-q", "-m", "world revert: "+clean)
	return err
}

func cleanRelPath(relpath string) (string, error) {
	relpath = strings.TrimSpace(relpath)
	if relpath == "" {
		return "", errors.New("path is required")
	}
	if filepath.IsAbs(relpath) {
		return "", fmt.Errorf("path %q must be relative", relpath)
	}
	clean := filepath.Clean(relpath)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes repository root", relpath)
	}
	return filepath.ToSlash(clean), nil
}

func gitOutput(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, gitError(err, stderr.String())
	}
	return out, nil
}

func hasCommits(ctx context.Context, dir string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--verify", "-q", "HEAD")
	cmd.Env = gitEnv()
	return cmd.Run() == nil
}

func gitEnv() []string {
	return append(os.Environ(), "LC_ALL=C", "LANG=C")
}

func gitError(err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, stderr)
}
