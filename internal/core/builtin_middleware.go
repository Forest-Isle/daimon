package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// GateMiddleware enforces a permission Gate before invoking the next handler.
// If the Gate returns DecisionApprove, the configured Approver is consulted.
// On DecisionDeny or a denied approval, ErrDenied is converted into a
// ToolResult so the model can observe the policy outcome.
func GateMiddleware(gate Gate, approver Approver) ToolMiddleware {
	if gate == nil {
		gate = AllowAllGate{}
	}
	if approver == nil {
		approver = AutoApprover{}
	}
	return func(next ToolHandler) ToolHandler {
		return func(ctx context.Context, call ToolCall) (ToolResult, error) {
			req := PermissionRequest{ToolName: call.Name, Input: call.Input}
			decision, reason, err := gate.Inspect(ctx, req)
			if err != nil {
				return ToolResult{UseID: call.ID, Error: "gate error: " + err.Error()}, nil
			}
			switch decision {
			case DecisionDeny:
				return ToolResult{
					UseID: call.ID,
					Error: "denied by policy: " + reason,
				}, nil
			case DecisionApprove:
				ok, err := approver.Approve(ctx, req, reason)
				if err != nil {
					return ToolResult{UseID: call.ID, Error: "approver error: " + err.Error()}, nil
				}
				if !ok {
					return ToolResult{UseID: call.ID, Error: "user rejected: " + reason}, nil
				}
			case DecisionAllow:
				// fall through
			}
			return next(ctx, call)
		}
	}
}

// TraceToolMiddleware emits Tool* events on the bus, enriched with
// duration. The Agent loop already emits before/after events; this
// middleware adds a per-handler boundary that lets sinks correlate
// elapsed time exactly even when other middleware adds latency.
func TraceToolMiddleware(sink EventSink) ToolMiddleware {
	if sink == nil {
		sink = NullSink
	}
	return func(next ToolHandler) ToolHandler {
		return func(ctx context.Context, call ToolCall) (ToolResult, error) {
			start := time.Now()
			res, err := next(ctx, call)
			res.Duration = time.Since(start)
			sink.Emit(Event{
				Kind:    EventToolResult,
				Time:    time.Now(),
				Payload: traceFrame{Tool: call.Name, ID: call.ID, Duration: res.Duration, Err: errString(err)},
			})
			return res, err
		}
	}
}

// TimeoutToolMiddleware applies a per-tool timeout via context.WithTimeout.
// A zero d disables the timeout. Cancelled tools surface as errors that the
// model can observe.
func TimeoutToolMiddleware(d time.Duration) ToolMiddleware {
	return func(next ToolHandler) ToolHandler {
		if d <= 0 {
			return next
		}
		return func(ctx context.Context, call ToolCall) (ToolResult, error) {
			ctx, cancel := context.WithTimeout(ctx, d)
			defer cancel()
			return next(ctx, call)
		}
	}
}

// CacheToolMiddleware caches read-only tool results in memory keyed by
// (tool name, sha256(input)). Identical tool calls within a single Run
// avoid duplicate work — measurable savings for redundant file reads.
//
// The cache is intentionally per-Agent (not shared) so isolation between
// runs is preserved. It only caches read-only tools.
func CacheToolMiddleware(reg *ToolRegistry) ToolMiddleware {
	type entry struct {
		res ToolResult
	}
	var (
		mu sync.RWMutex
		m  = make(map[string]entry)
	)
	return func(next ToolHandler) ToolHandler {
		return func(ctx context.Context, call ToolCall) (ToolResult, error) {
			t, ok := reg.Lookup(call.Name)
			if !ok || !t.ReadOnly() {
				return next(ctx, call)
			}
			key := cacheKey(call.Name, call.Input)
			mu.RLock()
			e, hit := m[key]
			mu.RUnlock()
			if hit {
				e.res.UseID = call.ID
				if e.res.Metadata == nil {
					e.res.Metadata = map[string]any{}
				}
				e.res.Metadata["cache"] = "hit"
				return e.res, nil
			}
			res, err := next(ctx, call)
			if err == nil && res.Error == "" {
				mu.Lock()
				m[key] = entry{res: res}
				mu.Unlock()
			}
			return res, err
		}
	}
}

func cacheKey(name string, input []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(name))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(input)
	return hex.EncodeToString(h.Sum(nil))
}

type traceFrame struct {
	Tool     string        `json:"tool"`
	ID       string        `json:"id"`
	Duration time.Duration `json:"duration"`
	Err      string        `json:"err,omitempty"`
}

func errString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
