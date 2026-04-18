package agent

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// GitContextProvider collects git repository state for injection into
// cognitive agent prompts. Unlike hook.GitContextInjector (which only
// works for the simple agent path via Runtime hooks), this provider
// is called directly during the cognitive PERCEIVE phase.
type GitContextProvider struct {
	timeout time.Duration
}

// NewGitContextProvider creates a provider with a 5-second default timeout.
func NewGitContextProvider() *GitContextProvider {
	return &GitContextProvider{timeout: 5 * time.Second}
}

// Collect gathers branch, uncommitted files, and recent commits from
// the git repo at dir. Returns nil if dir is not inside a git repository.
func (g *GitContextProvider) Collect(dir string) *GitState {
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()

	if !g.isGitRepo(ctx, dir) {
		return nil
	}

	state := &GitState{}

	state.Branch = g.runGit(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")

	if status := g.runGit(ctx, dir, "status", "--short"); status != "" {
		for _, line := range strings.Split(status, "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				state.UncommittedFiles = append(state.UncommittedFiles, trimmed)
			}
		}
	}

	if logOutput := g.runGit(ctx, dir, "log", "--oneline", "-5"); logOutput != "" {
		for _, line := range strings.Split(logOutput, "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				state.RecentCommits = append(state.RecentCommits, trimmed)
			}
		}
	}

	state.RawContent = g.format(state)

	slog.Debug("git context collected",
		"branch", state.Branch,
		"uncommitted", len(state.UncommittedFiles),
		"commits", len(state.RecentCommits),
	)

	return state
}

func (g *GitContextProvider) isGitRepo(ctx context.Context, dir string) bool {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--git-dir")
	cmd.Dir = dir
	return cmd.Run() == nil
}

func (g *GitContextProvider) runGit(ctx context.Context, dir string, args ...string) string {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		slog.Debug("git command failed", "args", args, "err", err)
		return ""
	}
	return strings.TrimSpace(stdout.String())
}

func (g *GitContextProvider) format(state *GitState) string {
	var sb strings.Builder

	if state.Branch != "" {
		fmt.Fprintf(&sb, "Branch: %s\n", state.Branch)
	}

	if len(state.UncommittedFiles) > 0 {
		sb.WriteString("Uncommitted changes:\n")
		for _, f := range state.UncommittedFiles {
			fmt.Fprintf(&sb, "  %s\n", f)
		}
	}

	if len(state.RecentCommits) > 0 {
		sb.WriteString("Recent commits:\n")
		for _, c := range state.RecentCommits {
			fmt.Fprintf(&sb, "  %s\n", c)
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}
