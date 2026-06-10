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

	// Thinking / Signature carry an extended-thinking block on an assistant
	// message so it can be replayed verbatim (the API verifies the signature).
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
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
	// ThinkingBudget enables extended thinking when > 0, reserving this many
	// tokens for reasoning. 0 disables thinking entirely (zero behavior change).
	ThinkingBudget int `json:"thinking_budget,omitempty"`
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
	// StopAbnormal marks a stop that is neither a clean end nor a tool call —
	// e.g. content filtering or an unrecognized provider finish reason. It must
	// not be treated as a successful completion.
	StopAbnormal StopReason = "abnormal"
)

// CompletionResponse is the full response from the LLM.
type CompletionResponse struct {
	Text       string
	ToolCalls  []ToolUseBlock
	StopReason StopReason
	// Thinking / Signature hold the extended-thinking block, if any. Signature
	// must be retained and replayed verbatim on the next request.
	Thinking  string
	Signature string
}

// StreamDelta is a chunk of a streaming response.
type StreamDelta struct {
	Text       string
	Thinking   string         // incremental thinking text (delta)
	Signature  string         // set on the final delta when a thinking block was produced
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

// Provider is the LLM backend interface.
type Provider interface {
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
	Stream(ctx context.Context, req CompletionRequest) (StreamIterator, error)
}
