package tool

import (
	"context"
	"strconv"
	"sync/atomic"
	"time"
)

// ToolActivityEvent describes one lifecycle moment of a tool invocation.
// ID is stable across the matching start (Done=false) and done (Done=true)
// events so a consumer can correlate them even under concurrent tool runs.
type ToolActivityEvent struct {
	ID       string
	Done     bool
	Result   *ToolResult   // nil on start
	Err      error         // nil on start; the tool's error on done
	Duration time.Duration // wall time of the invocation, set on done
}

// ToolActivityReporter receives non-blocking notifications about tool
// execution lifecycle. Implementations must never block tool execution.
type ToolActivityReporter interface {
	ReportToolActivity(ctx context.Context, call *ToolCall, evt ToolActivityEvent)
}

// ActivityInterceptor reports tool-execution start and finish to a reporter.
// It never alters the call or the result, so it is safe at the front of the
// chain, ahead of permission checks.
type ActivityInterceptor struct {
	reporter ToolActivityReporter
}

// NewActivityInterceptor creates an ActivityInterceptor. A nil reporter makes
// Intercept a transparent pass-through.
func NewActivityInterceptor(reporter ToolActivityReporter) *ActivityInterceptor {
	return &ActivityInterceptor{reporter: reporter}
}

func (a *ActivityInterceptor) Name() string { return "activity" }

var activityCounter uint64

func newActivityID() string {
	return "act_" + strconv.FormatUint(atomic.AddUint64(&activityCounter, 1), 10)
}

func (a *ActivityInterceptor) Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
	if a.reporter == nil {
		return next(ctx, call)
	}
	id := newActivityID()
	a.reporter.ReportToolActivity(ctx, call, ToolActivityEvent{ID: id})
	start := time.Now()
	result, err := next(ctx, call)
	a.reporter.ReportToolActivity(ctx, call, ToolActivityEvent{
		ID:       id,
		Done:     true,
		Result:   result,
		Err:      err,
		Duration: time.Since(start),
	})
	return result, err
}
