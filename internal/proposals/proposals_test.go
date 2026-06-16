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
	if pending[0].ActionKind != ActionKindEpisode || pending[1].ActionKind != ActionKindEpisode {
		t.Fatalf("blank ActionKind must default to episode: %#v", pending)
	}
}

func TestCreateGetActionKindRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.Create(ctx, Proposal{
		ID:         "promote",
		Title:      "Promote distilled skill: Foo",
		Body:       "body",
		ActionKind: ActionKindPromoteSkill,
		ActionRef:  "foo",
		CreatedAt:  1,
	}); err != nil {
		t.Fatalf("Create(promote): %v", err)
	}
	got, err := s.Get(ctx, "promote")
	if err != nil {
		t.Fatalf("Get(promote): %v", err)
	}
	if got.ActionKind != ActionKindPromoteSkill || got.ActionRef != "foo" {
		t.Fatalf("action fields did not round-trip: %#v", got)
	}

	if err := s.Create(ctx, Proposal{ID: "default", Title: "Default", CreatedAt: 1}); err != nil {
		t.Fatalf("Create(default): %v", err)
	}
	got, err = s.Get(ctx, "default")
	if err != nil {
		t.Fatalf("Get(default): %v", err)
	}
	if got.ActionKind != ActionKindEpisode || got.ActionRef != "" {
		t.Fatalf("blank ActionKind must default to episode with empty ref: %#v", got)
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

func TestPendingPromoteRefs(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	for _, p := range []Proposal{
		{ID: "live", Title: "Promote distilled skill: Live", ActionKind: ActionKindPromoteSkill, ActionRef: "live", CreatedAt: 1},
		{ID: "episode", Title: "Episode", ActionKind: ActionKindEpisode, ActionRef: "episode-ref", CreatedAt: 1},
		{ID: "empty", Title: "Empty", ActionKind: ActionKindPromoteSkill, ActionRef: "", CreatedAt: 1},
		{ID: "expired", Title: "Expired", ActionKind: ActionKindPromoteSkill, ActionRef: "expired", CreatedAt: 1, ExpiresAt: 100},
		{ID: "accepted", Title: "Accepted", ActionKind: ActionKindPromoteSkill, ActionRef: "accepted", CreatedAt: 1},
	} {
		if err := s.Create(ctx, p); err != nil {
			t.Fatalf("Create(%s): %v", p.ID, err)
		}
	}
	if err := s.Decide(ctx, "accepted", StateAccepted, 2); err != nil {
		t.Fatal(err)
	}

	refs, err := s.PendingPromoteRefs(ctx, 200)
	if err != nil {
		t.Fatalf("PendingPromoteRefs: %v", err)
	}
	if len(refs) != 1 || !refs["live"] {
		t.Fatalf("want only live promote ref, got %#v", refs)
	}
	live, err := s.PendingPromoteRefs(ctx, 50)
	if err != nil {
		t.Fatalf("PendingPromoteRefs(live): %v", err)
	}
	if !live["expired"] {
		t.Fatalf("expired ref should still be live before expiry: %#v", live)
	}
}

func TestRecentlyDismissedPromoteRefs(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	for _, p := range []Proposal{
		{ID: "old", Title: "Old Name", ActionKind: ActionKindPromoteSkill, ActionRef: "old", CreatedAt: 1},
		{ID: "boundary", Title: "Boundary Name", ActionKind: ActionKindPromoteSkill, ActionRef: "boundary", CreatedAt: 1},
		{ID: "recent", Title: "Recent Name", ActionKind: ActionKindPromoteSkill, ActionRef: "recent", CreatedAt: 1},
		{ID: "accepted", Title: "Accepted Name", ActionKind: ActionKindPromoteSkill, ActionRef: "accepted", CreatedAt: 1},
		{ID: "episode", Title: "Episode", ActionKind: ActionKindEpisode, ActionRef: "episode-ref", CreatedAt: 1},
		{ID: "empty", Title: "Empty", ActionKind: ActionKindPromoteSkill, ActionRef: "", CreatedAt: 1},
	} {
		if err := s.Create(ctx, p); err != nil {
			t.Fatalf("Create(%s): %v", p.ID, err)
		}
	}
	if err := s.Decide(ctx, "old", StateDismissed, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(ctx, "boundary", StateDismissed, 300); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(ctx, "recent", StateDismissed, 500); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(ctx, "accepted", StateAccepted, 500); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(ctx, "episode", StateDismissed, 500); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(ctx, "empty", StateDismissed, 500); err != nil {
		t.Fatal(err)
	}

	refs, err := s.RecentlyDismissedPromoteRefs(ctx, 300)
	if err != nil {
		t.Fatalf("RecentlyDismissedPromoteRefs: %v", err)
	}
	if len(refs) != 2 || !refs["boundary"] || !refs["recent"] {
		t.Fatalf("want boundary+recent promote refs only, got %#v", refs)
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
