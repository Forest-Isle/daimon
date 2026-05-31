package core

import "context"

// LLMRequest is the model invocation payload sent to a Provider.
// Tools are passed by reference (the registry) so the provider can render
// them in whatever shape the underlying API expects.
type LLMRequest struct {
	Model     string
	System    string
	Messages  []Message
	Tools     []ToolSchema
	MaxTokens int
}

// LLMResponse is what a provider returns. Streaming providers also expose
// the same shape after consuming the iterator.
type LLMResponse struct {
	Text       string
	ToolCalls  []ToolCall
	StopReason StopReason
}

// LLMChunk is a single streaming delta from a provider.
type LLMChunk struct {
	Text     string
	ToolCall *ToolCall // non-nil when a tool_use block is complete
	Done     bool
	Stop     StopReason
}

// Provider is the only LLM contract the core depends on. Implementations
// must be safe for concurrent use.
type Provider interface {
	Complete(ctx context.Context, req LLMRequest) (*LLMResponse, error)
	Stream(ctx context.Context, req LLMRequest) (Stream, error)
}

// Stream yields LLMChunks until Done is true or an error occurs.
type Stream interface {
	Next(ctx context.Context) (LLMChunk, error)
	Close() error
}
