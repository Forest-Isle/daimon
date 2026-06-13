package gateway

import (
	"context"

	"github.com/Forest-Isle/daimon/internal/action"
	"github.com/Forest-Isle/daimon/internal/tool"
	"github.com/Forest-Isle/daimon/internal/values"
)

// valueGate is the action pipeline's value segment. It permits a non-low-risk
// action when either a human is in the loop (an explicitly interactive channel,
// where the permission engine itself is the human-approval authority) or a
// recorded value decision / earned trust covers the action's domain. Otherwise
// it refuses autonomous release, triggering the ask-once flow.
//
// Trust boundary: the gate is fail-closed. Only an *explicitly* stamped Local
// channel counts as interactive; an unstamped or autonomous (internal/scheduled/
// background/remote) context must produce a covering value/trust source or the
// action is blocked. This guards against a path that forgets to stamp the class
// silently inheriting the permissive interactive default.
//
// Scope granularity: coverage is keyed by the action context (tool name),
// matching the trust ledger so values and trust align. This is intentionally
// coarse for the first cut — one active value in a tool's domain permits
// autonomous non-low-risk calls to that tool — but it is fail-closed by default
// (no entry → blocked). Per-call input scoping (matching the value statement
// against the specific command) is a later refinement; until then the gate only
// trusts values the user explicitly approved via the values tool.
type valueGate struct {
	values *values.Store
	trust  *action.Store
}

func newValueGate(v *values.Store, trust *action.Store) valueGate {
	return valueGate{values: v, trust: trust}
}

func (g valueGate) Permit(ctx context.Context, class action.Class, contextKey string) (string, bool) {
	// Interactive channels: the human is present and the permission engine is the
	// human-approval authority for the call, so that approval is the permitting
	// signature. Fail closed — only an explicitly stamped Local class qualifies.
	if cls, ok := tool.ChannelClassFromContextOK(ctx); ok && cls == tool.ToolChannelLocal {
		return "interactive", true
	}

	// Autonomous: a covering active value decision permits the action.
	if g.values != nil {
		if e, ok := g.values.Lookup(contextKey); ok {
			return "value:" + e.ID, true
		}
	}

	// Or earned trust above ask-every (rare for non-reversible, kept for parity
	// with the blueprint's "trust:L<n>" permission source).
	if g.trust != nil {
		if lvl, err := g.trust.TrustLevel(ctx, class, contextKey); err == nil && lvl > action.AskEvery {
			return "trust:" + lvl.String(), true
		}
	}

	return "", false
}
