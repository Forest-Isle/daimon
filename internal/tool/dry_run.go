package tool

import "context"

type dryRunCtxKey struct{}

// WithDryRun marks the context as a dry run. Under it, the action layer
// short-circuits governed (side-effecting) tool calls to record-only: the tool
// is not executed and no trust/undo/hold state changes. Read-only calls are
// unaffected and still execute, so a dry-run caller can observe the world it
// reasons over without changing it.
//
// This is the substrate for the mind.Shadow brain ("行动全部 dry-run"). It is
// fail-closed: only a caller that explicitly opts in carries the flag, so a
// production request context never dry-runs and real actions always execute.
func WithDryRun(ctx context.Context) context.Context {
	return context.WithValue(ctx, dryRunCtxKey{}, true)
}

// IsDryRun reports whether the context was marked for a dry run by WithDryRun.
func IsDryRun(ctx context.Context) bool {
	v, _ := ctx.Value(dryRunCtxKey{}).(bool)
	return v
}
