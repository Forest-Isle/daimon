package agent

import "context"

// ToolDefinition describes a tool for the LLM.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// CompletionMessage is a single message in a completion request.
type CompletionMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolUseID  string         `json:"tool_use_id,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	ToolInput  string         `json:"tool_input,omitempty"`
	ToolBlocks []ToolUseBlock `json:"tool_blocks,omitempty"`
}

// ToolUseBlock represents a tool_use block in an assistant message.
type ToolUseBlock struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"` // raw JSON
}

// CompletionRequest is sent to the LLM provider.
type CompletionRequest struct {
	Model          string              `json:"model"`
	System         string              `json:"system"`
	Messages       []CompletionMessage `json:"messages"`
	Tools          []ToolDefinition    `json:"tools"`
	MaxTokens      int                 `json:"max_tokens"`
	ToolChoice     string              `json:"tool_choice,omitempty"`
	ResponseFormat *ResponseFormat     `json:"response_format,omitempty"`
}

type ResponseFormat struct {
	Type       string      `json:"type"`
	JSONSchema *JSONSchema `json:"json_schema,omitempty"`
}

type JSONSchema struct {
	Name   string `json:"name"`
	Schema any    `json:"schema"`
	Strict bool   `json:"strict,omitempty"`
}

// StopReason indicates why the LLM stopped generating.
type StopReason string

const (
	StopEndTurn  StopReason = "end_turn"
	StopToolUse  StopReason = "tool_use"
	StopMaxToken StopReason = "max_tokens"
)

// CompletionResponse is the full response from the LLM.
type CompletionResponse struct {
	Text       string
	ToolCalls  []ToolUseBlock
	StopReason StopReason
}

// StreamDelta is a chunk of a streaming response.
type StreamDelta struct {
	Text       string
	ToolCall   *ToolUseBlock  // non-nil when a tool_use block is complete (first one, for compat)
	ToolCalls  []ToolUseBlock // all tool_use blocks from the final message
	Done       bool
	StopReason StopReason
}

// StreamIterator yields streaming deltas.
type StreamIterator interface {
	Next() (StreamDelta, error)
	Close()
}

// PendingToolBlockSource is an optional interface for StreamIterator
// implementations that can emit tool_use blocks before the stream completes.
// Used by speculative execution to launch read-only tools early.
type PendingToolBlockSource interface {
	PendingToolBlocks() []PendingToolBlock
}

// Provider is the LLM backend interface.
type Provider interface {
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
	Stream(ctx context.Context, req CompletionRequest) (StreamIterator, error)
}
