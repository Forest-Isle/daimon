package tool

import "context"

// ToolActivityReporter receives non-blocking notifications about tool
// execution lifecycle. done=false fires just before a tool runs; done=true
// fires after it returns. Implementations must never block tool execution.
type ToolActivityReporter interface {
	ReportToolActivity(ctx context.Context, call *ToolCall, done bool)
}

// ActivityInterceptor reports tool-execution start and finish to a reporter.
// It is purely informational — it never alters the call or the result — so it
// is safe to place at the front of the chain, ahead of permission checks, to
// surface activity for tools across all permission tiers (including ones the
// user has set to auto-approve).
type ActivityInterceptor struct {
	reporter ToolActivityReporter
}

// NewActivityInterceptor creates an ActivityInterceptor. A nil reporter makes
// Intercept a transparent pass-through.
func NewActivityInterceptor(reporter ToolActivityReporter) *ActivityInterceptor {
	return &ActivityInterceptor{reporter: reporter}
}

func (a *ActivityInterceptor) Name() string { return "activity" }

func (a *ActivityInterceptor) Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
	if a.reporter == nil {
		return next(ctx, call)
	}
	a.reporter.ReportToolActivity(ctx, call, false)
	result, err := next(ctx, call)
	a.reporter.ReportToolActivity(ctx, call, true)
	return result, err
}
