package worktree

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/Forest-Isle/IronClaw/internal/tool"
)

func RegisterTools(registry *tool.Registry, repoPath string) {
	manager := NewWorktreeManager(repoPath)
	registry.Register(NewCreateTool(manager))
	registry.Register(NewDiffTool(manager))
	registry.Register(NewMergeTool(manager))
	registry.Register(NewListTool(manager))
}

type CreateTool struct {
	manager *WorktreeManager
}

type createInput struct {
	Name       string `json:"name"`
	BaseBranch string `json:"base_branch"`
}

func NewCreateTool(manager *WorktreeManager) *CreateTool {
	return &CreateTool{manager: manager}
}

func (t *CreateTool) Name() string { return "worktree_create" }

func (t *CreateTool) Description() string {
	return "Create an isolated git worktree for code changes when the agent needs to work safely in a separate branch."
}

func (t *CreateTool) RequiresApproval() bool { return true }

func (t *CreateTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Unique worktree name used for the path and feature branch.",
			},
			"base_branch": map[string]any{
				"type":        "string",
				"description": "Base branch to branch from. Currently informational; new worktrees are created with git worktree add -b feature/<name>.",
				"default":     "main",
			},
		},
		"required": []string{"name"},
	}
}

func (t *CreateTool) Execute(ctx context.Context, input []byte) (tool.Result, error) {
	var in createInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.Result{Error: "invalid input: " + err.Error()}, nil
	}
	if in.Name == "" {
		return tool.Result{Error: "name is required"}, nil
	}

	info, err := t.manager.Create(ctx, in.Name)
	if err != nil {
		return tool.Result{Error: err.Error()}, nil
	}

	payload, err := json.Marshal(info)
	if err != nil {
		return tool.Result{}, fmt.Errorf("marshal create result: %w", err)
	}
	return tool.Result{
		Output:   string(payload),
		Metadata: map[string]any{"path": info.Path, "branch": info.Branch},
	}, nil
}

type DiffTool struct {
	manager *WorktreeManager
}

type diffInput struct {
	Path string `json:"path"`
}

func NewDiffTool(manager *WorktreeManager) *DiffTool {
	return &DiffTool{manager: manager}
}

func (t *DiffTool) Name() string           { return "worktree_diff" }
func (t *DiffTool) RequiresApproval() bool { return false }
func (t *DiffTool) IsReadOnly() bool       { return true }

func (t *DiffTool) Description() string {
	return "Show the current git diff for a managed worktree relative to main..HEAD."
}

func (t *DiffTool) Capabilities() tool.ToolCapabilities {
	return tool.ToolCapabilities{
		IsReadOnly:      true,
		IsDestructive:   false,
		RequiresNetwork: false,
		ApprovalMode:    "never",
	}
}

func (t *DiffTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute or repo-relative path to the worktree.",
			},
		},
		"required": []string{"path"},
	}
}

func (t *DiffTool) Execute(ctx context.Context, input []byte) (tool.Result, error) {
	var in diffInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.Result{Error: "invalid input: " + err.Error()}, nil
	}
	if in.Path == "" {
		return tool.Result{Error: "path is required"}, nil
	}

	diff, err := t.manager.GetDiff(ctx, in.Path)
	if err != nil {
		return tool.Result{Error: err.Error()}, nil
	}
	return tool.Result{
		Output:   diff,
		Metadata: map[string]any{"path": filepath.Clean(in.Path)},
	}, nil
}

type MergeTool struct {
	manager *WorktreeManager
}

type mergeInput struct {
	Path string `json:"path"`
}

func NewMergeTool(manager *WorktreeManager) *MergeTool {
	return &MergeTool{manager: manager}
}

func (t *MergeTool) Name() string { return "worktree_merge" }

func (t *MergeTool) Description() string {
	return "Merge a managed worktree branch back into the main repository branch and remove the worktree."
}

func (t *MergeTool) RequiresApproval() bool { return true }

func (t *MergeTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute or repo-relative path to the worktree to merge and remove.",
			},
		},
		"required": []string{"path"},
	}
}

func (t *MergeTool) Execute(ctx context.Context, input []byte) (tool.Result, error) {
	var in mergeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.Result{Error: "invalid input: " + err.Error()}, nil
	}
	if in.Path == "" {
		return tool.Result{Error: "path is required"}, nil
	}

	info, err := t.manager.findWorktreeByPath(ctx, in.Path)
	if err != nil {
		return tool.Result{Error: err.Error()}, nil
	}
	branch := trimBranchRef(info.Branch)
	if err := t.manager.MergeAndCleanup(ctx, in.Path, branch); err != nil {
		return tool.Result{Error: err.Error()}, nil
	}

	payload, err := json.Marshal(map[string]any{
		"path":    filepath.Clean(in.Path),
		"branch":  branch,
		"merged":  true,
		"cleaned": true,
	})
	if err != nil {
		return tool.Result{}, fmt.Errorf("marshal merge result: %w", err)
	}
	return tool.Result{
		Output:   string(payload),
		Metadata: map[string]any{"path": filepath.Clean(in.Path), "branch": branch},
	}, nil
}

type ListTool struct {
	manager *WorktreeManager
}

func NewListTool(manager *WorktreeManager) *ListTool {
	return &ListTool{manager: manager}
}

func (t *ListTool) Name() string           { return "worktree_list" }
func (t *ListTool) RequiresApproval() bool { return false }
func (t *ListTool) IsReadOnly() bool       { return true }

func (t *ListTool) Description() string {
	return "List all active git worktrees managed by the repository, including branch and lock status."
}

func (t *ListTool) Capabilities() tool.ToolCapabilities {
	return tool.ToolCapabilities{
		IsReadOnly:      true,
		IsDestructive:   false,
		RequiresNetwork: false,
		ApprovalMode:    "never",
	}
}

func (t *ListTool) InputSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *ListTool) Execute(ctx context.Context, input []byte) (tool.Result, error) {
	if len(input) > 0 && string(input) != "{}" {
		var noop map[string]any
		if err := json.Unmarshal(input, &noop); err != nil {
			return tool.Result{Error: "invalid input: " + err.Error()}, nil
		}
	}

	worktrees, err := t.manager.List(ctx)
	if err != nil {
		return tool.Result{Error: err.Error()}, nil
	}
	payload, err := json.Marshal(worktrees)
	if err != nil {
		return tool.Result{}, fmt.Errorf("marshal list result: %w", err)
	}
	return tool.Result{
		Output:   string(payload),
		Metadata: map[string]any{"count": len(worktrees)},
	}, nil
}

func Available() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func trimBranchRef(branch string) string {
	const prefix = "refs/heads/"
	if branch == "" {
		return ""
	}
	if !filepath.IsAbs(branch) && len(branch) >= len(prefix) && branch[:len(prefix)] == prefix {
		return branch[len(prefix):]
	}
	return branch
}
