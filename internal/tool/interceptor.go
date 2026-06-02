package tool

import (
	"context"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
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
	ctx, span := observability.StartSpan(ctx, "tool.execute",
		trace.WithAttributes(
			attribute.String("tool.name", call.ToolName),
			attribute.String("session.id", call.SessionID),
		))
	start := time.Now()
	defer span.End()

	if len(c.interceptors) == 0 {
		result, err := final(ctx, call)
		recordToolExecution(ctx, span, start, call, result, err)
		return result, err
	}
	handler := final
	for i := len(c.interceptors) - 1; i >= 0; i-- {
		ic := c.interceptors[i]
		next := handler
		handler = func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
			return ic.Intercept(ctx, call, next)
		}
	}
	result, err := handler(ctx, call)
	recordToolExecution(ctx, span, start, call, result, err)
	return result, err
}

func recordToolExecution(
	ctx context.Context,
	span trace.Span,
	start time.Time,
	call *ToolCall,
	result *ToolResult,
	err error,
) {
	status := "success"
	if err != nil {
		status = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else if result != nil && result.Error != "" {
		status = "error"
	}
	observability.ToolExecutionDuration.Record(ctx, time.Since(start).Milliseconds(),
		metric.WithAttributes(
			attribute.String("tool.name", call.ToolName),
			attribute.String("status", status),
		))
}
