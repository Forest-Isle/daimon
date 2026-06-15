package tool

import (
	"context"
	"sync"
)

// ActionVerification accumulates, for one episode, how its governed (non
// read-only) action calls fared: how many ran and how many were verified —
// objectively earned trust on this run (a successful reversible action; see the
// action interceptor). It is written per call by the action interceptor (looked
// up from the request context) and read once at episode close to derive the
// per-episode "unverified actions" signal.
//
// It is strictly observational: nothing reads it to change control flow. The
// mutex makes it safe even though episode tool calls are dispatched sequentially
// today — so a future move to concurrent dispatch cannot corrupt the counts.
type ActionVerification struct {
	mu       sync.Mutex
	governed int
	verified int
}

// Record notes one governed action call and whether it was verified.
func (a *ActionVerification) Record(verified bool) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.governed++
	if verified {
		a.verified++
	}
}

// Snapshot returns the governed and verified counts accumulated so far.
func (a *ActionVerification) Snapshot() (governed, verified int) {
	if a == nil {
		return 0, 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.governed, a.verified
}

type actionCollectorCtxKey struct{}

// WithActionCollector attaches a per-episode action-verification collector to the
// context so the action interceptor can report each governed call into it. A nil
// collector is not attached (callers that do not care leave the context clean).
func WithActionCollector(ctx context.Context, c *ActionVerification) context.Context {
	if c == nil {
		return ctx
	}
	return context.WithValue(ctx, actionCollectorCtxKey{}, c)
}

// ActionCollectorFromContext returns the collector attached by WithActionCollector,
// or nil when none is present (e.g. the legacy loop, or any caller that did not
// install one) — in which case the interceptor simply records nothing.
func ActionCollectorFromContext(ctx context.Context) *ActionVerification {
	c, _ := ctx.Value(actionCollectorCtxKey{}).(*ActionVerification)
	return c
}
