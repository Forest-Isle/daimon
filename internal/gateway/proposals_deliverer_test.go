package gateway

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Forest-Isle/daimon/internal/proposals"
	"github.com/Forest-Isle/daimon/internal/store"
)

func newDelivererStore(t *testing.T) *proposals.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "deliver.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return proposals.NewStore(db.DB)
}

// undeliveredIDs asserts the query succeeds (so a broken query cannot false-pass a
// len==0 check) and returns the ids in order.
func undeliveredIDs(t *testing.T, ps *proposals.Store, now int64) []string {
	t.Helper()
	items, err := ps.ListUndelivered(context.Background(), now)
	if err != nil {
		t.Fatalf("ListUndelivered: %v", err)
	}
	ids := make([]string, len(items))
	for i, p := range items {
		ids[i] = p.ID
	}
	return ids
}

func TestDeliverPendingSendsEachUndeliveredOnce(t *testing.T) {
	ps := newDelivererStore(t)
	ctx := context.Background()
	for _, p := range []proposals.Proposal{
		{ID: "a", Title: "a", Urgency: 1, CreatedAt: 10},
		{ID: "b", Title: "b", Urgency: 3, CreatedAt: 20},
	} {
		if err := ps.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	var sent []string
	d := &proposalDeliverer{
		store: ps,
		send:  func(_ context.Context, p proposals.Proposal) error { sent = append(sent, p.ID); return nil },
		now:   func() int64 { return 100 },
	}

	if err := d.deliverPending(ctx); err != nil {
		t.Fatalf("deliverPending: %v", err)
	}
	// Urgency order: b (3) before a (1).
	if len(sent) != 2 || sent[0] != "b" || sent[1] != "a" {
		t.Fatalf("expected [b a] sent in urgency order, got %v", sent)
	}

	// A second cycle must deliver nothing — both are marked delivered.
	sent = nil
	if err := d.deliverPending(ctx); err != nil {
		t.Fatalf("deliverPending (2): %v", err)
	}
	if len(sent) != 0 {
		t.Fatalf("delivered proposals must not be re-pushed, got %v", sent)
	}
}

func TestDeliverPendingSkipsOnSendFailure(t *testing.T) {
	ps := newDelivererStore(t)
	ctx := context.Background()
	if err := ps.Create(ctx, proposals.Proposal{ID: "a", Title: "a", CreatedAt: 10}); err != nil {
		t.Fatal(err)
	}

	attempts := 0
	d := &proposalDeliverer{
		store: ps,
		send: func(_ context.Context, _ proposals.Proposal) error {
			attempts++
			if attempts == 1 {
				return fmt.Errorf("transient")
			}
			return nil
		},
		now: func() int64 { return 100 },
	}

	// First cycle: send fails → left undelivered.
	if err := d.deliverPending(ctx); err != nil {
		t.Fatalf("deliverPending: %v", err)
	}
	if undel := undeliveredIDs(t, ps, 200); len(undel) != 1 {
		t.Fatalf("a failed send must leave the proposal undelivered, got %v", undel)
	}

	// Second cycle: send succeeds → retried and marked.
	if err := d.deliverPending(ctx); err != nil {
		t.Fatalf("deliverPending (2): %v", err)
	}
	if undel := undeliveredIDs(t, ps, 200); len(undel) != 0 {
		t.Fatalf("a successful retry must mark the proposal delivered, got %v", undel)
	}
}

func TestUndeliveredExcludesDecidedAndExpired(t *testing.T) {
	ps := newDelivererStore(t)
	ctx := context.Background()
	if err := ps.Create(ctx, proposals.Proposal{ID: "live", Title: "live", CreatedAt: 10}); err != nil {
		t.Fatal(err)
	}
	if err := ps.Create(ctx, proposals.Proposal{ID: "expired", Title: "expired", CreatedAt: 10, ExpiresAt: 50}); err != nil {
		t.Fatal(err)
	}
	if err := ps.Create(ctx, proposals.Proposal{ID: "decided", Title: "decided", CreatedAt: 10}); err != nil {
		t.Fatal(err)
	}
	if err := ps.Decide(ctx, "decided", proposals.StateAccepted, 20); err != nil {
		t.Fatal(err)
	}

	if undel := undeliveredIDs(t, ps, 100); len(undel) != 1 || undel[0] != "live" {
		t.Fatalf("only the live undecided proposal is undelivered, got %v", undel)
	}
}

func TestMarkDeliveredIsIdempotent(t *testing.T) {
	ps := newDelivererStore(t)
	ctx := context.Background()
	if err := ps.Create(ctx, proposals.Proposal{ID: "a", Title: "a", CreatedAt: 10}); err != nil {
		t.Fatal(err)
	}
	if err := ps.MarkDelivered(ctx, "a", 100); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	// Second mark stamps nothing (already delivered) and must not error.
	if err := ps.MarkDelivered(ctx, "a", 999); err != nil {
		t.Fatalf("second MarkDelivered: %v", err)
	}
	if undel := undeliveredIDs(t, ps, 200); len(undel) != 0 {
		t.Fatalf("marked proposal must be delivered, got %v", undel)
	}
}

// TestDeliverPendingConcurrentSendsOnce proves the mu serialization closes the
// read-then-act window: many cycles firing at once must each send a given
// proposal no more than once in total.
func TestDeliverPendingConcurrentSendsOnce(t *testing.T) {
	ps := newDelivererStore(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := ps.Create(ctx, proposals.Proposal{ID: fmt.Sprintf("p%d", i), Title: "t", CreatedAt: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}

	var sends atomic.Int64
	d := &proposalDeliverer{
		store: ps,
		send:  func(context.Context, proposals.Proposal) error { sends.Add(1); return nil },
		now:   func() int64 { return 100 },
	}

	const cycles = 6
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < cycles; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = d.deliverPending(ctx)
		}()
	}
	close(start)
	wg.Wait()

	// 5 proposals, serialized cycles: each delivered exactly once → 5 total sends.
	if got := sends.Load(); got != 5 {
		t.Fatalf("concurrent cycles must send each of 5 proposals once, got %d sends", got)
	}
	if undel := undeliveredIDs(t, ps, 200); len(undel) != 0 {
		t.Fatalf("all proposals must be delivered, got %v", undel)
	}
}
