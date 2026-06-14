package gateway

import (
	"context"
	"testing"

	"github.com/Forest-Isle/daimon/internal/action"
	"github.com/Forest-Isle/daimon/internal/tool"
	"github.com/Forest-Isle/daimon/internal/values"
)

func TestValueGateInteractiveOnlyWhenExplicitLocal(t *testing.T) {
	g := newValueGate(values.NewStore(t.TempDir()), nil)

	// Explicitly stamped Local → interactive permit.
	ctx := tool.WithChannelClass(context.Background(), tool.ToolChannelLocal)
	if ref, ok := g.Permit(ctx, action.Irreversible, "bash"); !ok || ref != "interactive" {
		t.Fatalf("explicit local should permit interactive, got %q %v", ref, ok)
	}

	// Unstamped context must fail closed (treated as untrusted), NOT default to
	// interactive — even though ChannelClassFromContext would report local.
	if _, ok := g.Permit(context.Background(), action.Irreversible, "bash"); ok {
		t.Fatal("unstamped context must not be auto-permitted (fail closed)")
	}

	// Autonomous channel with no covering value → blocked.
	ictx := tool.WithChannelClass(context.Background(), tool.ToolChannelInternal)
	if _, ok := g.Permit(ictx, action.Irreversible, "bash"); ok {
		t.Fatal("autonomous action without a value should be blocked")
	}
}

func TestValueGateAutonomousPermittedByValue(t *testing.T) {
	store := values.NewStore(t.TempDir())
	if _, err := store.Add(context.Background(), values.Entry{
		Domain: "mailer", Statement: "may send routine status mail autonomously", Confidence: 0.9,
	}); err != nil {
		t.Fatal(err)
	}
	g := newValueGate(store, nil)

	// A covering value permits a COMPENSABLE autonomous action (compensable =
	// reversible-with-effort, so a value can authorize it).
	ictx := tool.WithChannelClass(context.Background(), tool.ToolChannelScheduled)
	ref, ok := g.Permit(ictx, action.Compensable, "mailer")
	if !ok {
		t.Fatal("covering value should permit the autonomous compensable action")
	}
	if ref == "" || ref[:6] != "value:" {
		t.Fatalf("permit ref = %q, want value:<id>", ref)
	}
}

// TestValueGateIrreversibleNeverAutonomous verifies CF5 / invariant #4: an
// irreversible action is never released autonomously — not by a covering value,
// not by earned trust. Only a present human (interactive Local channel) may
// authorize it.
func TestValueGateIrreversibleNeverAutonomous(t *testing.T) {
	store := values.NewStore(t.TempDir())
	if _, err := store.Add(context.Background(), values.Entry{
		Domain: "bash", Statement: "may run destructive bash", Confidence: 0.95,
	}); err != nil {
		t.Fatal(err)
	}
	g := newValueGate(store, nil)

	// Even with a covering value, an autonomous irreversible action is blocked.
	ictx := tool.WithChannelClass(context.Background(), tool.ToolChannelScheduled)
	if ref, ok := g.Permit(ictx, action.Irreversible, "bash"); ok {
		t.Fatalf("irreversible must not be auto-permitted by a value, got ref=%q", ref)
	}
	// But a present human (interactive Local) may still authorize it.
	lctx := tool.WithChannelClass(context.Background(), tool.ToolChannelLocal)
	if ref, ok := g.Permit(lctx, action.Irreversible, "bash"); !ok || ref != "interactive" {
		t.Fatalf("interactive human should authorize irreversible, got %q %v", ref, ok)
	}
}
