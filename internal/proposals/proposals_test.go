package proposals

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/daimon/internal/store"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "proposals.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db.DB)
}

func TestRecentlyDismissedTitles(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for _, p := range []Proposal{
		{ID: "old", Title: "old idea", CreatedAt: 1},
		{ID: "boundary", Title: "boundary idea", CreatedAt: 1},
		{ID: "recent", Title: "recent idea", CreatedAt: 1},
		{ID: "accepted", Title: "accepted idea", CreatedAt: 1},
	} {
		if err := s.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Decide(ctx, "old", StateDismissed, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(ctx, "boundary", StateDismissed, 300); err != nil { // decided_at == since
		t.Fatal(err)
	}
	if err := s.Decide(ctx, "recent", StateDismissed, 500); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(ctx, "accepted", StateAccepted, 500); err != nil {
		t.Fatal(err)
	}

	// since=300: includes the boundary (>= is inclusive) and recent dismissals;
	// excludes the older dismissal (decided_at 100) and the accepted one.
	got, err := s.RecentlyDismissedTitles(ctx, 300)
	if err != nil {
		t.Fatalf("RecentlyDismissedTitles: %v", err)
	}
	if len(got) != 2 || !got["recent idea"] || !got["boundary idea"] {
		t.Fatalf("want {recent idea, boundary idea} (inclusive boundary), got %v", got)
	}
}

func TestCreateAndListPending(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Urgency ordering: higher urgency first, then older created_at first.
	if err := s.Create(ctx, Proposal{Title: "low", Urgency: 1, CreatedAt: 100, ExpiresAt: 0}); err != nil {
		t.Fatalf("Create(low): %v", err)
	}
	if err := s.Create(ctx, Proposal{Title: "high", Urgency: 3, CreatedAt: 200, ExpiresAt: 0}); err != nil {
		t.Fatalf("Create(high): %v", err)
	}
	if err := s.Create(ctx, Proposal{Title: "expired", Urgency: 3, CreatedAt: 50, ExpiresAt: 150}); err != nil {
		t.Fatalf("Create(expired): %v", err)
	}

	pending, err := s.ListPending(ctx, 175)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending len = %d, want 2 (expired excluded): %#v", len(pending), pending)
	}
	if pending[0].Title != "high" || pending[1].Title != "low" {
		t.Fatalf("urgency ordering wrong: %#v", pending)
	}
	if pending[0].ID == "" {
		t.Fatal("Create did not assign an id")
	}
}

func TestPendingTitles(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.Create(ctx, Proposal{Title: "alpha", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, Proposal{ID: "p_beta", Title: "beta", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	// An expired-but-undecided proposal (nothing has transitioned it out of
	// pending) must not block its title — it should be absent from pending titles
	// once now is past its expiry, so the same idea can be re-proposed.
	if err := s.Create(ctx, Proposal{ID: "p_gamma", Title: "gamma", CreatedAt: 1, ExpiresAt: 100}); err != nil {
		t.Fatal(err)
	}
	// A decided proposal must not appear among pending titles.
	if err := s.Decide(ctx, "p_beta", StateDismissed, 5); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	titles, err := s.PendingTitles(ctx, 200)
	if err != nil {
		t.Fatalf("PendingTitles: %v", err)
	}
	if !titles["alpha"] {
		t.Fatalf("alpha should be pending: %#v", titles)
	}
	if titles["beta"] {
		t.Fatalf("beta is dismissed and must not be pending: %#v", titles)
	}
	if titles["gamma"] {
		t.Fatalf("gamma is expired at now=200 and must not block re-proposal: %#v", titles)
	}
	// Before its expiry, gamma is still live and must dedup.
	live, err := s.PendingTitles(ctx, 50)
	if err != nil {
		t.Fatalf("PendingTitles(live): %v", err)
	}
	if !live["gamma"] {
		t.Fatalf("gamma should be live at now=50: %#v", live)
	}
}

func TestDecide(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.Create(ctx, Proposal{ID: "p1", Title: "decide me", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(ctx, "p1", StateAccepted, 99); err != nil {
		t.Fatalf("Decide(accept): %v", err)
	}
	// It is no longer pending.
	pending, err := s.ListPending(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("accepted proposal still pending: %#v", pending)
	}
	// Deciding again must error — it is already decided.
	if err := s.Decide(ctx, "p1", StateDismissed, 100); err == nil {
		t.Fatal("second Decide should error (already decided)")
	}
	// Unknown id must error.
	if err := s.Decide(ctx, "missing", StateAccepted, 1); err == nil {
		t.Fatal("Decide on unknown id should error")
	}
	// Invalid terminal state must error before touching the row.
	if err := s.Create(ctx, Proposal{ID: "p2", Title: "x", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(ctx, "p2", "pending", 1); err == nil {
		t.Fatal("Decide to a non-terminal state should error")
	}
}
