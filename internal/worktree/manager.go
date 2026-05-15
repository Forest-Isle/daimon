package worktree

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrNotGitRepo       = errors.New("not a git repository")
	ErrWorktreeExists   = errors.New("worktree already exists")
	ErrWorktreeNotFound = errors.New("worktree not found")
)

type WorktreeInfo struct {
	Path      string    `json:"path"`
	Branch    string    `json:"branch"`
	HEAD      string    `json:"head"`
	IsBare    bool      `json:"is_bare"`
	IsLocked  bool      `json:"is_locked"`
	CreatedAt time.Time `json:"created_at"`
}

type WorktreeManager struct {
	repoPath    string
	stagingRoot string
	logger      *slog.Logger
}

func NewWorktreeManager(repoPath string) *WorktreeManager {
	cleanRepoPath := filepath.Clean(repoPath)
	logger := slog.With("component", "worktree_manager", "repo_path", cleanRepoPath)

	if _, err := runGit(context.Background(), cleanRepoPath, "rev-parse", "--is-inside-work-tree"); err != nil {
		logger.Warn("repo path is not a git repo", "err", err)
		return &WorktreeManager{repoPath: cleanRepoPath, stagingRoot: filepath.Join(cleanRepoPath, ".codex-staging"), logger: logger}
	}

	topLevel, err := runGit(context.Background(), cleanRepoPath, "rev-parse", "--show-toplevel")
	if err != nil {
		logger.Warn("failed to resolve repo root", "err", err)
		return &WorktreeManager{repoPath: cleanRepoPath, stagingRoot: filepath.Join(cleanRepoPath, ".codex-staging"), logger: logger}
	}

	topLevel = strings.TrimSpace(topLevel)
	return &WorktreeManager{
		repoPath:    topLevel,
		stagingRoot: filepath.Join(topLevel, ".codex-staging"),
		logger:      slog.With("component", "worktree_manager", "repo_path", topLevel),
	}
}

func (m *WorktreeManager) validateRepo(ctx context.Context) error {
	if _, err := runGit(ctx, m.repoPath, "rev-parse", "--is-inside-work-tree"); err != nil {
		return fmt.Errorf("%w: %v", ErrNotGitRepo, err)
	}
	return nil
}

func (m *WorktreeManager) Create(ctx context.Context, name string) (*WorktreeInfo, error) {
	if err := m.validateRepo(ctx); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	worktreePath := filepath.Join(m.stagingRoot, name)
	if err := os.MkdirAll(m.stagingRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create staging root: %w", err)
	}

	if _, err := os.Stat(worktreePath); err == nil {
		return nil, ErrWorktreeExists
	}

	branch := "feature/" + name
	if _, err := runGit(ctx, m.repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		return nil, ErrWorktreeExists
	}

	if _, err := runGit(ctx, m.repoPath, "worktree", "add", worktreePath, "-b", branch); err != nil {
		if strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "is already checked out") {
			return nil, fmt.Errorf("%w: %s", ErrWorktreeExists, name)
		}
		return nil, fmt.Errorf("create worktree %q: %w", name, err)
	}

	info, err := m.findWorktreeByPath(ctx, worktreePath)
	if err != nil {
		return nil, err
	}
	m.logger.Info("worktree created", "path", info.Path, "branch", info.Branch)
	return &info, nil
}

func (m *WorktreeManager) GetDiff(ctx context.Context, wtPath string) (string, error) {
	if err := m.validateRepo(ctx); err != nil {
		return "", err
	}
	if !m.ValidatePath(wtPath) {
		return "", ErrWorktreeNotFound
	}
	out, err := runGit(ctx, filepath.Clean(wtPath), "diff", "main..HEAD")
	if err != nil {
		return "", fmt.Errorf("get diff for %q: %w", wtPath, err)
	}
	return out, nil
}

func (m *WorktreeManager) MergeAndCleanup(ctx context.Context, wtPath, branch string) error {
	if err := m.validateRepo(ctx); err != nil {
		return err
	}

	info, err := m.findWorktreeByPath(ctx, wtPath)
	if err != nil {
		return err
	}
	if branch == "" {
		branch = strings.TrimPrefix(info.Branch, "refs/heads/")
	}
	if branch == "" {
		return fmt.Errorf("branch is required")
	}

	if _, err := runGit(ctx, m.repoPath, "checkout", "main"); err != nil {
		return fmt.Errorf("checkout %q: %w", "main", err)
	}
	if _, err := runGit(ctx, m.repoPath, "merge", branch); err != nil {
		return fmt.Errorf("merge %q: %w", branch, err)
	}
	if _, err := runGit(ctx, m.repoPath, "worktree", "remove", filepath.Clean(wtPath)); err != nil {
		return fmt.Errorf("remove worktree %q: %w", wtPath, err)
	}
	m.logger.Info("worktree merged and cleaned up", "path", wtPath, "branch", branch)
	return nil
}

func (m *WorktreeManager) List(ctx context.Context) ([]WorktreeInfo, error) {
	if err := m.validateRepo(ctx); err != nil {
		return nil, err
	}
	out, err := runGit(ctx, m.repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("list worktrees: %w", err)
	}
	return parsePorcelainList(out), nil
}

func (m *WorktreeManager) CleanupOrphans(ctx context.Context) error {
	if err := m.validateRepo(ctx); err != nil {
		return err
	}

	worktrees, err := m.List(ctx)
	if err != nil {
		return err
	}

	for _, wt := range worktrees {
		if filepath.Clean(wt.Path) == filepath.Clean(m.repoPath) {
			continue
		}

		branch := strings.TrimPrefix(wt.Branch, "refs/heads/")
		if branch == "" {
			continue
		}

		if _, err := runGit(ctx, m.repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
			continue
		}

		m.logger.Warn("removing orphaned worktree", "path", wt.Path, "branch", branch)
		if _, err := runGit(ctx, m.repoPath, "worktree", "remove", "--force", wt.Path); err != nil {
			return fmt.Errorf("remove orphan worktree %q: %w", wt.Path, err)
		}
	}

	if _, err := runGit(ctx, m.repoPath, "worktree", "prune"); err != nil {
		return fmt.Errorf("prune worktrees: %w", err)
	}
	return nil
}

func (m *WorktreeManager) ValidatePath(path string) bool {
	cleanPath := filepath.Clean(path)
	worktrees, err := m.List(context.Background())
	if err != nil {
		return false
	}
	for _, wt := range worktrees {
		if filepath.Clean(wt.Path) == cleanPath {
			return true
		}
	}
	return false
}

func (m *WorktreeManager) findWorktreeByPath(ctx context.Context, wtPath string) (WorktreeInfo, error) {
	worktrees, err := m.List(ctx)
	if err != nil {
		return WorktreeInfo{}, err
	}
	cleanPath := filepath.Clean(wtPath)
	for _, wt := range worktrees {
		if filepath.Clean(wt.Path) == cleanPath {
			return wt, nil
		}
	}
	return WorktreeInfo{}, ErrWorktreeNotFound
}

func parsePorcelainList(output string) []WorktreeInfo {
	scanner := bufio.NewScanner(strings.NewReader(output))
	var (
		worktrees []WorktreeInfo
		current   *WorktreeInfo
	)

	flush := func() {
		if current != nil {
			if info, err := os.Stat(current.Path); err == nil {
				current.CreatedAt = info.ModTime()
			}
			worktrees = append(worktrees, *current)
			current = nil
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}

		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current = &WorktreeInfo{Path: strings.TrimSpace(strings.TrimPrefix(line, "worktree "))}
		case current == nil:
			continue
		case strings.HasPrefix(line, "HEAD "):
			current.HEAD = strings.TrimSpace(strings.TrimPrefix(line, "HEAD "))
		case strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimSpace(strings.TrimPrefix(line, "branch "))
		case line == "bare":
			current.IsBare = true
		case strings.HasPrefix(line, "locked"):
			current.IsLocked = true
		}
	}
	flush()
	return worktrees
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
