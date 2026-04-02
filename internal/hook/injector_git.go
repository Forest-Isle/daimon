package hook

import (
	"bytes"
	"context"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// GitContextInjector is an OnUserMessage handler that injects git branch
// and status information into the system prompt.
type GitContextInjector struct {
	TimeoutMs       int
	IncludeDiffStat bool
}

// NewGitContextInjector creates a git context injector with the given config.
func NewGitContextInjector(config map[string]any) *GitContextInjector {
	g := &GitContextInjector{
		TimeoutMs: 2000,
	}
	if v, ok := config["timeout_ms"]; ok {
		switch val := v.(type) {
		case int:
			g.TimeoutMs = val
		case float64:
			g.TimeoutMs = int(val)
		}
	}
	if v, ok := config["include_diff_stat"]; ok {
		if b, ok := v.(bool); ok {
			g.IncludeDiffStat = b
		}
	}
	return g
}

func (g *GitContextInjector) OnUserMessage(ctx context.Context, _ OnUserMessageEvent) (OnUserMessageResult, error) {
	timeout := time.Duration(g.TimeoutMs) * time.Millisecond
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Check if we're in a git repo
	if err := exec.CommandContext(execCtx, "git", "rev-parse", "--git-dir").Run(); err != nil {
		// Not a git repo — silently skip
		return OnUserMessageResult{}, nil
	}

	var parts []string

	// Get current branch
	branch, err := runGitCmd(execCtx, "git", "branch", "--show-current")
	if err != nil {
		slog.Warn("hook: git branch failed", "err", err)
		return OnUserMessageResult{}, nil
	}
	if branch != "" {
		parts = append(parts, "Branch: "+branch)
	}

	// Get status
	status, err := runGitCmd(execCtx, "git", "status", "--short")
	if err != nil {
		slog.Warn("hook: git status failed", "err", err)
	} else if status != "" {
		parts = append(parts, "Changes:\n"+status)
	} else {
		parts = append(parts, "Working tree clean")
	}

	// Optional: diff stat
	if g.IncludeDiffStat {
		diffStat, err := runGitCmd(execCtx, "git", "diff", "--stat", "HEAD~1")
		if err == nil && diffStat != "" {
			parts = append(parts, "Recent changes:\n"+diffStat)
		}
	}

	if len(parts) == 0 {
		return OnUserMessageResult{}, nil
	}

	gitContext := "Git: " + strings.Join(parts, " | ")
	return OnUserMessageResult{
		InjectedContext: []string{gitContext},
	}, nil
}

func runGitCmd(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}
