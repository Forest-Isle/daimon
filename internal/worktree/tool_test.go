package worktree

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/tool"
)

func TestWorktreeManagerAndTools(t *testing.T) {
	ctx := context.Background()
	repo := setupGitRepo(t)
	manager := NewWorktreeManager(repo)

	createTool := NewCreateTool(manager)
	listTool := NewListTool(manager)
	diffTool := NewDiffTool(manager)
	mergeTool := NewMergeTool(manager)

	createRes, err := createTool.Execute(ctx, mustJSON(t, map[string]any{
		"name":        "feature-a",
		"base_branch": "main",
	}))
	if err != nil {
		t.Fatalf("create tool err: %v", err)
	}
	if createRes.Error != "" {
		t.Fatalf("create tool returned error: %s", createRes.Error)
	}

	var created WorktreeInfo
	if err := json.Unmarshal([]byte(createRes.Output), &created); err != nil {
		t.Fatalf("unmarshal create output: %v", err)
	}
	if created.Path == "" {
		t.Fatalf("expected created path")
	}
	if trimBranchRef(created.Branch) != "feature/feature-a" {
		t.Fatalf("unexpected branch: %s", created.Branch)
	}

	listRes, err := listTool.Execute(ctx, []byte(`{}`))
	if err != nil {
		t.Fatalf("list tool err: %v", err)
	}
	if listRes.Error != "" {
		t.Fatalf("list tool returned error: %s", listRes.Error)
	}
	var listed []WorktreeInfo
	if err := json.Unmarshal([]byte(listRes.Output), &listed); err != nil {
		t.Fatalf("unmarshal list output: %v", err)
	}
	if len(listed) < 2 {
		t.Fatalf("expected at least main repo and one worktree, got %d", len(listed))
	}

	diffRes, err := diffTool.Execute(ctx, mustJSON(t, map[string]any{"path": created.Path}))
	if err != nil {
		t.Fatalf("diff tool err: %v", err)
	}
	if diffRes.Error != "" {
		t.Fatalf("diff tool returned error: %s", diffRes.Error)
	}
	if strings.TrimSpace(diffRes.Output) != "" {
		t.Fatalf("expected empty diff for new worktree, got %q", diffRes.Output)
	}

	writeFile(t, filepath.Join(created.Path, "feature.txt"), "hello\n")
	git(t, created.Path, "add", "feature.txt")
	git(t, created.Path, "commit", "-m", "add feature")

	mergeRes, err := mergeTool.Execute(ctx, mustJSON(t, map[string]any{"path": created.Path}))
	if err != nil {
		t.Fatalf("merge tool err: %v", err)
	}
	if mergeRes.Error != "" {
		t.Fatalf("merge tool returned error: %s", mergeRes.Error)
	}
	if _, err := os.Stat(created.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected worktree path removed, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); err != nil {
		t.Fatalf("expected merged file in repo: %v", err)
	}
}

func TestManagerErrors(t *testing.T) {
	ctx := context.Background()
	nonRepo := t.TempDir()
	manager := NewWorktreeManager(nonRepo)

	if _, err := manager.List(ctx); !errors.Is(err, ErrNotGitRepo) {
		t.Fatalf("expected ErrNotGitRepo, got %v", err)
	}

	repo := setupGitRepo(t)
	manager = NewWorktreeManager(repo)

	first, err := manager.Create(ctx, "duplicate")
	if err != nil {
		t.Fatalf("first create err: %v", err)
	}
	if _, err := manager.Create(ctx, "duplicate"); !errors.Is(err, ErrWorktreeExists) {
		t.Fatalf("expected ErrWorktreeExists, got %v", err)
	}
	if _, err := manager.GetDiff(ctx, filepath.Join(repo, "missing")); !errors.Is(err, ErrWorktreeNotFound) {
		t.Fatalf("expected ErrWorktreeNotFound, got %v", err)
	}
	if manager.ValidatePath(filepath.Join(repo, "missing")) {
		t.Fatalf("expected ValidatePath false for missing path")
	}
	if !manager.ValidatePath(first.Path) {
		t.Fatalf("expected ValidatePath true for created worktree")
	}
}

func TestRegisterTools(t *testing.T) {
	repo := setupGitRepo(t)
	registry := tool.NewRegistry()
	RegisterTools(registry, repo)

	for _, name := range []string{"worktree_create", "worktree_diff", "worktree_merge", "worktree_list"} {
		if _, err := registry.Get(name); err != nil {
			t.Fatalf("expected registered tool %s: %v", name, err)
		}
	}
}

func setupGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.com")
	writeFile(t, filepath.Join(repo, "README.md"), "base\n")
	git(t, repo, "add", "README.md")
	git(t, repo, "commit", "-m", "initial")
	return repo
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGit(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %v failed: %v", args, err)
	}
	return out
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return data
}
