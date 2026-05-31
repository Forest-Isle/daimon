package core

import (
	"context"
	"fmt"
	"time"
)

// RetryPolicy decides whether to retry a failed tool call and with what delay.
type RetryPolicy struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
}

// DefaultRetryPolicy is a sensible retry config for network-bound tools (HTTP,
// browser). Bash and file tools are not retried by default (idempotency risk).
var DefaultRetryPolicy = RetryPolicy{MaxRetries: 2, BaseDelay: 500 * time.Millisecond, MaxDelay: 5 * time.Second}

// RetryMiddleware wraps a ToolHandler with retry logic for tools whose names
// match `tools`. Only errors (hard failures) trigger retries; soft failures
// (result.Error != "") are left for the model to observe and self-correct.
//
// Use separate RetryMiddleware instances with different RetryPolicy per tool
// group (e.g. idempotent GET vs state-changing POST).
func RetryMiddleware(tools []string, policy RetryPolicy) ToolMiddleware {
	allow := make(map[string]bool, len(tools))
	for _, t := range tools {
		allow[t] = true
	}
	return func(next ToolHandler) ToolHandler {
		return func(ctx context.Context, call ToolCall) (ToolResult, error) {
			if !allow[call.Name] {
				return next(ctx, call)
			}
			var lastErr error
			for attempt := 0; attempt <= policy.MaxRetries; attempt++ {
				if attempt > 0 {
					delay := policy.BaseDelay * time.Duration(1<<(attempt-1))
					if delay > policy.MaxDelay {
						delay = policy.MaxDelay
					}
					select {
					case <-ctx.Done():
						return ToolResult{UseID: call.ID, Error: ctx.Err().Error()}, ctx.Err()
					case <-time.After(delay):
					}
				}
				res, err := next(ctx, call)
				if err == nil {
					return res, nil
				}
				lastErr = err
				if attempt < policy.MaxRetries {
					res.Error = fmt.Sprintf("RETRY %d/%d: %v", attempt+1, policy.MaxRetries, err)
				}
				// On last attempt, return the result so the model sees the error.
				if attempt == policy.MaxRetries {
					res.Error = fmt.Sprintf("RETRY EXHAUSTED after %d attempts: %v", policy.MaxRetries+1, err)
					return res, nil
				}
			}
			return ToolResult{UseID: call.ID, Error: lastErr.Error()}, lastErr
		}
	}
}
