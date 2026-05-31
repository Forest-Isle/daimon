package core

import (
	"encoding/json"
	"time"
)

// Role for chat messages. Mirrors the small set needed by the loop.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is a single turn in the conversation. A message can carry plain
// text (Content), the assistant's tool_use intents (ToolCalls), or a tool
// result (ToolUseID + Content). Keeping a single struct avoids ad-hoc
// branching that plagued the legacy code.
type Message struct {
	Role      Role       `json:"role"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"` // assistant → tools
	ToolUseID string     `json:"tool_use_id,omitempty"` // tool result → use
}

// ToolCall is one tool_use request from the assistant.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult is the structured outcome of a tool invocation.
type ToolResult struct {
	UseID    string         `json:"use_id"`
	Output   string         `json:"output"`
	Error    string         `json:"error,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Duration time.Duration  `json:"duration"`
}

// StopReason marks why an iteration ended.
type StopReason string

const (
	StopEndTurn  StopReason = "end_turn"
	StopToolUse  StopReason = "tool_use"
	StopMaxTurns StopReason = "max_turns"
	StopError    StopReason = "error"
)
