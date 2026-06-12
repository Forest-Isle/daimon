package agent

import (
	"context"

	"github.com/Forest-Isle/daimon/internal/memory"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/tool"
)

// SubagentContext provides isolation and inheritance control for sub-agents.
// It separates what is shared (read-only references) from what is isolated
// (scoped tool registry, cancel func, tracking metadata).
type SubagentContext struct {
	// --- Isolation layer ---

	// ToolRegistry is the scoped tool set for this sub-agent.
	// agent_* tools are always excluded to prevent recursion.
	ToolRegistry *tool.Registry

	// Permission controls how this sub-agent handles tool approval.
	Permission PermissionMode

	// Cancel cancels this sub-agent's context without affecting the parent.
	Cancel context.CancelFunc

	// AbortOnParent if true, this sub-agent is cancelled when the parent is cancelled.
	AbortOnParent bool

	// --- Inheritance layer (read-only references) ---

	// ParentMessages is the parent's message history (read-only snapshot).
	// Only populated for fork agents; nil for spawn agents.
	ParentMessages []session.Message

	// SystemPrompt is the parent's system prompt string (for fork reuse).
	SystemPrompt string

	// Memory is the shared memory store (read-only queries by sub-agents).
	Memory memory.Store

	// Sessions is the shared session manager.
	Sessions *session.Manager

	// DB is the shared database.
	DB *store.DB

	// --- Tracking ---

	// AgentID uniquely identifies this sub-agent invocation.
	AgentID string

	// ParentID is the agent ID of the parent that spawned this sub-agent.
	// Empty string for top-level agents.
	ParentID string

	// Depth is the nesting level (0 = top-level, 1 = first sub-agent, etc.).
	Depth int

	// ChainID groups all agents in a single invocation chain for tracing.
	ChainID string
}

// subagentContextKey is the context.Context key for SubagentContext.
type subagentContextKey struct{}

// SubagentContextToCtx stores a SubagentContext in the context.
func SubagentContextToCtx(ctx context.Context, sc *SubagentContext) context.Context {
	return context.WithValue(ctx, subagentContextKey{}, sc)
}

// SubagentContextFromCtx retrieves the SubagentContext from the context.
func SubagentContextFromCtx(ctx context.Context) *SubagentContext {
	sc, _ := ctx.Value(subagentContextKey{}).(*SubagentContext)
	return sc
}
