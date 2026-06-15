package mind

import (
	"context"
	"log/slog"

	"github.com/Forest-Isle/daimon/internal/config"
)

// NewProviderFromConfig builds the LLM provider described by an LLMConfig: an
// OpenAI-compatible or Claude backend, wrapped in the retry provider when retries
// are configured. It is the single construction point shared by the gateway
// runtime and offline tools (e.g. `daimon replay --against`), so adding a backend
// is a one-place change.
func NewProviderFromConfig(llm config.LLMConfig) Provider {
	var p Provider
	if llm.Provider == "openai" || llm.Provider == "openai-compatible" {
		p = NewOpenAIProvider(llm.APIKey, llm.Model, llm.BaseURL)
		slog.Info("LLM provider: openai-compatible", "model", llm.Model)
	} else {
		p = NewClaudeProvider(llm.APIKey, llm.Model, llm.BaseURL)
		slog.Info("LLM provider: claude", "model", llm.Model)
	}
	if llm.Retry.MaxRetries > 0 {
		p = NewRetryProvider(p, llm.Retry)
	}
	return p
}

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

// Usage reports per-call token consumption from a single provider response. A
// zero value means the provider did not report usage (an older backend, or a
// streamed response whose usage chunk was absent) — callers treat zero as
// "unknown", never as "free". It is populated best-effort and additively; it
// never influences control flow, so an inaccurate or missing value cannot change
// what the agent does, only what the cost ledger records.
type Usage struct {
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int
}

// Add accumulates another call's usage into u, so a caller can sum the per-call
// usage of every provider call in a multi-step episode into one total.
func (u *Usage) Add(other Usage) {
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.CacheReadTokens += other.CacheReadTokens
	u.CacheCreationTokens += other.CacheCreationTokens
}

// CompletionResponse is the full response from the LLM.
type CompletionResponse struct {
	Text       string
	ToolCalls  []ToolUseBlock
	StopReason StopReason
	// Thinking / Signature hold the extended-thinking block, if any. Signature
	// must be retained and replayed verbatim on the next request.
	Thinking  string
	Signature string
	// Usage reports the tokens this call consumed (best-effort; see Usage).
	Usage Usage
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
	// Usage is set only on the final delta (Done) and reports the tokens this
	// streamed call consumed (best-effort; see Usage). Zero on non-final deltas.
	Usage Usage
}

// StreamIterator yields streaming deltas.
type StreamIterator interface {
	Next() (StreamDelta, error)
	Close()
}

// Caps describes the negotiated capabilities of a Provider. The prompt assembly
// (and, later, the model router and shadow) consult it so that provider-specific
// behaviour is declared by the provider rather than hard-coded at the call site —
// "swap the brain" without touching mind-external code.
type Caps struct {
	// CacheBreakpoints is the number of caller-placed prompt-cache boundaries the
	// provider honors (e.g. Anthropic ephemeral cache_control blocks). 0 means the
	// provider does no caller-placed prompt caching — caching is automatic or
	// absent — so the composer must NOT insert a cache-boundary marker, which would
	// otherwise leak into the prompt as literal text.
	CacheBreakpoints int
}

// Provider is the LLM backend interface.
type Provider interface {
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
	Stream(ctx context.Context, req CompletionRequest) (StreamIterator, error)
	// Capabilities reports what the provider supports so callers negotiate rather
	// than assume. Cheap and pure — safe to call per request.
	Capabilities() Caps
}
