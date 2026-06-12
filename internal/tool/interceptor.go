package tool

import (
	"context"
)

// InterceptorFunc is the function signature for the next step in the chain.
type InterceptorFunc func(ctx context.Context, call *ToolCall) (*ToolResult, error)

// ToolInterceptor wraps tool execution with additional behavior.
type ToolInterceptor interface {
	Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error)
	Name() string
}

// ToolCall carries all information about a pending tool invocation.
type ToolCall struct {
	ToolName     string
	Input        string
	SessionID    string
	Metadata     map[string]string
	Capabilities ToolCapabilities
	HookApproved bool // set to true when a pre-tool-use hook returns "allow"
}

// ToolResult wraps the output of a tool execution through the interceptor chain.
type ToolResult struct {
	Output   string
	Error    string
	Metadata map[string]string
}

// InterceptorChain composes interceptors into an ordered execution pipeline.
type InterceptorChain struct {
	interceptors []ToolInterceptor
}

// NewInterceptorChain creates a chain from the given interceptors.
func NewInterceptorChain(interceptors []ToolInterceptor) *InterceptorChain {
	return &InterceptorChain{interceptors: interceptors}
}

// Execute runs the interceptor chain, ending with the final function.
func (c *InterceptorChain) Execute(ctx context.Context, call *ToolCall, final InterceptorFunc) (*ToolResult, error) {
	if len(c.interceptors) == 0 {
		return final(ctx, call)
	}
	handler := final
	for i := len(c.interceptors) - 1; i >= 0; i-- {
		ic := c.interceptors[i]
		next := handler
		handler = func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
			return ic.Intercept(ctx, call, next)
		}
	}
	return handler(ctx, call)
}
