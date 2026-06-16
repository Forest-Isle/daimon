package gateway

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Forest-Isle/daimon/internal/attention"
	"github.com/Forest-Isle/daimon/internal/proposals"
	"github.com/Forest-Isle/daimon/internal/store"
)

// newCoordinatorHarness wires a real proposals store on a temp DB to recorded
// fire/feedback closures so each branch is observable without a live agent or
// Telegram. The clock is fixed so decided_at assertions are deterministic.
func newCoordinatorHarness(t *testing.T) (*proposalCoordinator, *proposals.Store, *[]string, *[]string, *[]attention.Feedback) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "coord.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ps := proposals.NewStore(db.DB)

	var fired []string
	var promoted []string
	var recorded []attention.Feedback
	c := &proposalCoordinator{
		store: ps,
		fire: func(_ context.Context, idem, goal, _, class string) error {
			fired = append(fired, idem+"|"+goal+"|"+class)
			return nil
		},
		promote: func(_ context.Context, slug string) error {
			promoted = append(promoted, slug)
			return nil
		},
		feedback: func(_ context.Context, fb attention.Feedback) error {
			recorded = append(recorded, fb)
			return nil
		},
		now: func() int64 { return 1000 },
	}
	return c, ps, &fired, &promoted, &recorded
}

func TestProposalAcceptFiresActionPlan(t *testing.T) {
	c, ps, fired, promoted, recorded := newCoordinatorHarness(t)
	ctx := context.Background()
	if err := ps.Create(ctx, proposals.Proposal{ID: "p1", Title: "prep", ActionPlan: "do the thing", Body: "ctx", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}

	if err := c.Accept(ctx, "p1"); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	// Episode fired with id as idempotency key, action plan as goal, "proposal" class.
	if len(*fired) != 1 || (*fired)[0] != "p1|do the thing|proposal" {
		t.Fatalf("expected one episode fired, got %v", *fired)
	}
	if len(*promoted) != 0 {
		t.Fatalf("episode proposal must not promote, got %v", *promoted)
	}
	if len(*recorded) != 0 {
		t.Fatalf("accept must not record feedback, got %v", *recorded)
	}
	// State transitioned to accepted (no longer pending).
	if pending, _ := ps.ListPending(ctx, 2000); len(pending) != 0 {
		t.Fatalf("accepted proposal must leave the pending queue, got %v", pending)
	}
}

func TestProposalAcceptIsExactlyOnce(t *testing.T) {
	c, ps, fired, _, _ := newCoordinatorHarness(t)
	ctx := context.Background()
	if err := ps.Create(ctx, proposals.Proposal{ID: "p1", ActionPlan: "go", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := c.Accept(ctx, "p1"); err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	// Second accept must gate on the state transition and fire nothing more.
	if err := c.Accept(ctx, "p1"); err == nil {
		t.Fatal("second Accept must fail (already decided)")
	}
	if len(*fired) != 1 {
		t.Fatalf("double-tap must fire exactly once, got %v", *fired)
	}
}

func TestProposalAcceptWithoutPlanFiresNothing(t *testing.T) {
	c, ps, fired, _, _ := newCoordinatorHarness(t)
	ctx := context.Background()
	if err := ps.Create(ctx, proposals.Proposal{ID: "p1", Title: "fyi", ActionPlan: "  ", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := c.Accept(ctx, "p1"); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if len(*fired) != 0 {
		t.Fatalf("informational proposal must fire no episode, got %v", *fired)
	}
}

func TestProposalDismissRecordsFeedback(t *testing.T) {
	c, ps, fired, promoted, recorded := newCoordinatorHarness(t)
	ctx := context.Background()
	if err := ps.Create(ctx, proposals.Proposal{ID: "p1", Title: "unwanted", ActionPlan: "x", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}

	if err := c.Dismiss(ctx, "p1"); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}

	if len(*fired) != 0 {
		t.Fatalf("dismiss must not fire an episode, got %v", *fired)
	}
	if len(*promoted) != 0 {
		t.Fatalf("dismiss must not promote, got %v", *promoted)
	}
	if len(*recorded) != 1 {
		t.Fatalf("dismiss must record exactly one feedback, got %v", *recorded)
	}
	fb := (*recorded)[0]
	if fb.EventID != "p1" || fb.GivenAction != "proposed" || fb.ExpectedAction != "ignore" || fb.Note != "unwanted" {
		t.Fatalf("unexpected feedback payload: %+v", fb)
	}
	if pending, _ := ps.ListPending(ctx, 2000); len(pending) != 0 {
		t.Fatalf("dismissed proposal must leave the pending queue, got %v", pending)
	}
}

func TestProposalDecideUnknownID(t *testing.T) {
	c, _, _, _, _ := newCoordinatorHarness(t)
	ctx := context.Background()
	if err := c.Accept(ctx, "nope"); err == nil {
		t.Fatal("accepting an unknown proposal must error")
	}
	if err := c.Dismiss(ctx, "nope"); err == nil {
		t.Fatal("dismissing an unknown proposal must error")
	}
}

// TestProposalAcceptConcurrentFiresOnce exercises the real Get/Decide race the
// exactly-once claim rests on: many goroutines accept the same proposal at once,
// and only the Decide that wins the WHERE state=pending update may fire.
func TestProposalAcceptConcurrentFiresOnce(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "race.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ps := proposals.NewStore(db.DB)
	ctx := context.Background()
	if err := ps.Create(ctx, proposals.Proposal{ID: "p1", ActionPlan: "go", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}

	var fireCount atomic.Int64
	c := &proposalCoordinator{
		store: ps,
		fire: func(_ context.Context, _, _, _, _ string) error {
			fireCount.Add(1)
			return nil
		},
		feedback: func(context.Context, attention.Feedback) error { return nil },
		now:      func() int64 { return 1000 },
	}

	const n = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = c.Accept(ctx, "p1")
		}(i)
	}
	close(start)
	wg.Wait()

	if got := fireCount.Load(); got != 1 {
		t.Fatalf("concurrent accept must fire exactly once, fired %d times", got)
	}
	wins := 0
	for _, e := range errs {
		if e == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("exactly one accept must succeed, got %d winners", wins)
	}
}

// TestProposalAcceptFireFailureLeavesAccepted documents the fire-failure window:
// when fire errors after Decide, the proposal is already accepted (not retryable
// through this path) and the error surfaces for the caller to log. Recovery is by
// re-proposal, not retry — asserted here as "no longer pending".
func TestProposalAcceptFireFailureLeavesAccepted(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "firefail.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ps := proposals.NewStore(db.DB)
	ctx := context.Background()
	if err := ps.Create(ctx, proposals.Proposal{ID: "p1", ActionPlan: "go", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	c := &proposalCoordinator{
		store:    ps,
		fire:     func(context.Context, string, string, string, string) error { return fmt.Errorf("boom") },
		feedback: func(context.Context, attention.Feedback) error { return nil },
		now:      func() int64 { return 1000 },
	}
	if err := c.Accept(ctx, "p1"); err == nil {
		t.Fatal("fire failure must surface as an error")
	}
	if pending, _ := ps.ListPending(ctx, 2000); len(pending) != 0 {
		t.Fatalf("proposal must be left accepted (not pending) after fire failure, got %v", pending)
	}
}

// TestProposalDecideRejectsExpired guards a stale inline button: a proposal past
// its expiry must not act, even though nothing has transitioned it to the expired
// state yet.
func TestProposalDecideRejectsExpired(t *testing.T) {
	c, ps, fired, _, recorded := newCoordinatorHarness(t)
	ctx := context.Background()
	// now() is fixed at 1000; expires_at 500 is in the past.
	if err := ps.Create(ctx, proposals.Proposal{ID: "p1", ActionPlan: "go", CreatedAt: 1, ExpiresAt: 500}); err != nil {
		t.Fatal(err)
	}
	if err := c.Accept(ctx, "p1"); err == nil {
		t.Fatal("accepting an expired proposal must error")
	}
	if err := c.Dismiss(ctx, "p1"); err == nil {
		t.Fatal("dismissing an expired proposal must error")
	}
	if len(*fired) != 0 || len(*recorded) != 0 {
		t.Fatalf("an expired proposal must neither fire nor record: fired=%v recorded=%v", *fired, *recorded)
	}
}

func TestProposalAcceptPromoteSkill(t *testing.T) {
	c, ps, fired, promoted, _ := newCoordinatorHarness(t)
	ctx := context.Background()
	if err := ps.Create(ctx, proposals.Proposal{
		ID:         "p1",
		Title:      "Promote distilled skill: Daily Summary",
		ActionKind: proposals.ActionKindPromoteSkill,
		ActionRef:  "daily-summary",
		ActionPlan: "must not fire",
		CreatedAt:  1,
	}); err != nil {
		t.Fatal(err)
	}

	if err := c.Accept(ctx, "p1"); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if len(*promoted) != 1 || (*promoted)[0] != "daily-summary" {
		t.Fatalf("expected deterministic promote, got %v", *promoted)
	}
	if len(*fired) != 0 {
		t.Fatalf("promote proposal must not fire an episode, got %v", *fired)
	}
}

func TestProposalAcceptPromoteSkillExactlyOnce(t *testing.T) {
	c, ps, _, promoted, _ := newCoordinatorHarness(t)
	ctx := context.Background()
	if err := ps.Create(ctx, proposals.Proposal{
		ID:         "p1",
		ActionKind: proposals.ActionKindPromoteSkill,
		ActionRef:  "daily-summary",
		CreatedAt:  1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.Accept(ctx, "p1"); err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	if err := c.Accept(ctx, "p1"); err == nil {
		t.Fatal("second Accept must fail (already decided)")
	}
	if len(*promoted) != 1 {
		t.Fatalf("double-tap must promote exactly once, got %v", *promoted)
	}
}

func TestProposalAcceptPromoteSkillEmptyRef(t *testing.T) {
	c, ps, fired, promoted, _ := newCoordinatorHarness(t)
	ctx := context.Background()
	if err := ps.Create(ctx, proposals.Proposal{
		ID:         "p1",
		ActionKind: proposals.ActionKindPromoteSkill,
		ActionRef:  " ",
		CreatedAt:  1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.Accept(ctx, "p1"); err == nil {
		t.Fatal("empty action_ref must error")
	}
	if len(*fired) != 0 || len(*promoted) != 0 {
		t.Fatalf("empty action_ref must run no action: fired=%v promoted=%v", *fired, *promoted)
	}
	got, err := ps.Get(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != proposals.StatePending {
		t.Fatalf("bad promote proposal must remain pending, got %q", got.State)
	}
}

func TestProposalAcceptPromoteSkillNilHandler(t *testing.T) {
	c, ps, fired, promoted, _ := newCoordinatorHarness(t)
	c.promote = nil
	ctx := context.Background()
	if err := ps.Create(ctx, proposals.Proposal{
		ID:         "p1",
		ActionKind: proposals.ActionKindPromoteSkill,
		ActionRef:  "daily-summary",
		CreatedAt:  1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.Accept(ctx, "p1"); err == nil {
		t.Fatal("nil promote handler must error")
	}
	if len(*fired) != 0 || len(*promoted) != 0 {
		t.Fatalf("nil promote handler must run no action: fired=%v promoted=%v", *fired, *promoted)
	}
	got, err := ps.Get(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != proposals.StatePending {
		t.Fatalf("unwired promote proposal must remain pending, got %q", got.State)
	}
}

func TestProposalAcceptUnknownActionKindFailsClosed(t *testing.T) {
	c, ps, fired, promoted, _ := newCoordinatorHarness(t)
	ctx := context.Background()
	if err := ps.Create(ctx, proposals.Proposal{
		ID:         "p1",
		ActionKind: "bogus",
		ActionPlan: "must not fire",
		ActionRef:  "must-not-promote",
		CreatedAt:  1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.Accept(ctx, "p1"); err == nil {
		t.Fatal("unknown action_kind must error")
	}
	if len(*fired) != 0 || len(*promoted) != 0 {
		t.Fatalf("unknown action_kind must run no action: fired=%v promoted=%v", *fired, *promoted)
	}
	got, err := ps.Get(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != proposals.StatePending {
		t.Fatalf("unknown action_kind must remain pending, got %q", got.State)
	}
}
