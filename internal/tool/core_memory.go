package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/memory"
)

// CoreMemoryTool lets the LLM actively manage its own persistent memory.
// Mirrors Mem0/Letta's agentic memory pattern: the agent decides what to
// remember, update, or forget based on conversation context.
type CoreMemoryTool struct {
	store    memory.Store
	lcMgr    *memory.LifecycleManager
}

// CoreMemoryInput is the JSON schema for the tool call.
type CoreMemoryInput struct {
	Action   string `json:"action"`   // "remember", "forget", "update"
	Content  string `json:"content"`  // the fact to store / update / delete
	Category string `json:"category"` // optional: "preference", "knowledge", "identity", "plan"
	FactID   string `json:"fact_id"`  // required for "forget" and "update"
}

func NewCoreMemoryTool(store memory.Store, lcMgr *memory.LifecycleManager) *CoreMemoryTool {
	return &CoreMemoryTool{store: store, lcMgr: lcMgr}
}

func (t *CoreMemoryTool) Name() string        { return "core_memory" }
func (t *CoreMemoryTool) Description() string { return "Manage persistent memory: remember new facts, update existing ones, or forget outdated information." }
func (t *CoreMemoryTool) RequiresApproval() bool { return true } // writes to memory, requires approval
func (t *CoreMemoryTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{IsDestructive: true}
}

func (t *CoreMemoryTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":   map[string]any{"type": "string", "enum": []string{"remember", "forget", "update"}},
			"content":  map[string]any{"type": "string", "description": "The fact or information to store"},
			"category": map[string]any{"type": "string", "description": "Optional: preference, knowledge, identity, plan"},
			"fact_id":  map[string]any{"type": "string", "description": "Required for forget and update actions"},
		},
		"required": []string{"action", "content"},
	}
}

func (t *CoreMemoryTool) Execute(ctx context.Context, input []byte) (Result, error) {
	var in CoreMemoryInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	switch in.Action {
	case "remember":
		return t.doRemember(ctx, in)
	case "forget":
		return t.doForget(ctx, in)
	case "update":
		return t.doUpdate(ctx, in)
	default:
		return Result{Error: fmt.Sprintf("unknown action: %s (use remember, forget, or update)", in.Action)}, nil
	}
}

func (t *CoreMemoryTool) doRemember(ctx context.Context, in CoreMemoryInput) (Result, error) {
	meta := map[string]string{}
	if in.Category != "" {
		meta["category"] = strings.ToLower(in.Category)
	}
	now := time.Now()
	entry := memory.Entry{
		ID:        fmt.Sprintf("core_%d", now.UnixNano()),
		Scope:     memory.ScopeUser,
		Content:   in.Content,
		Metadata:  meta,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := t.store.Save(ctx, entry); err != nil {
		return Result{Error: fmt.Sprintf("failed to remember: %v", err)}, nil
	}
	return Result{Output: fmt.Sprintf("Remembered: %s", in.Content)}, nil
}

func (t *CoreMemoryTool) doForget(ctx context.Context, in CoreMemoryInput) (Result, error) {
	if in.FactID == "" {
		return Result{Error: "fact_id is required for forget action"}, nil
	}
	if err := t.store.Delete(ctx, in.FactID); err != nil {
		return Result{Error: fmt.Sprintf("failed to forget: %v", err)}, nil
	}
	return Result{Output: fmt.Sprintf("Forgot fact %s", in.FactID)}, nil
}

func (t *CoreMemoryTool) doUpdate(ctx context.Context, in CoreMemoryInput) (Result, error) {
	if in.FactID == "" {
		return Result{Error: "fact_id is required for update action"}, nil
	}
	// Search for the existing fact to get its current version
	results, err := t.store.Search(ctx, memory.SearchQuery{
		Text:   in.FactID,
		Limit:  1,
		Scopes: []memory.MemoryScope{memory.ScopeUser, memory.ScopeSession, memory.ScopeGlobal},
	})
	if err != nil || len(results) == 0 {
		return Result{Error: fmt.Sprintf("fact %s not found for update", in.FactID)}, nil
	}
	currentVersion := results[0].Entry.Version
	if err := t.store.Update(ctx, in.FactID, in.Content, currentVersion); err != nil {
		return Result{Error: fmt.Sprintf("failed to update: %v", err)}, nil
	}
	return Result{Output: fmt.Sprintf("Updated fact %s: %s", in.FactID, in.Content)}, nil
}
