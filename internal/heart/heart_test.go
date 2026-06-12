package heart

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/store"
)

func openHeartTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "heart.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db.DB)
}

type recorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *recorder) handle(_ context.Context, ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func TestStorePersistDedup(t *testing.T) {
	s := openHeartTestStore(t)
	ctx := context.Background()

	in1, err := s.Persist(ctx, &Event{Source: "mail", Kind: "mail.received", DedupKey: "msg-1", OccurredAt: "2030-01-01 00:00:00"})
	if err != nil || !in1 {
		t.Fatalf("first persist inserted=%v err=%v", in1, err)
	}
	in2, err := s.Persist(ctx, &Event{Source: "mail", Kind: "mail.received", DedupKey: "msg-1", OccurredAt: "2030-01-01 00:00:01"})
	if err != nil {
		t.Fatalf("second persist err=%v", err)
	}
	if in2 {
		t.Fatal("duplicate dedup_key should not insert")
	}

	// keyless events never collapse
	for i := 0; i < 3; i++ {
		in, err := s.Persist(ctx, &Event{Source: "timer", Kind: "tick", OccurredAt: "2030-01-01 00:00:00"})
		if err != nil || !in {
			t.Fatalf("keyless persist %d inserted=%v err=%v", i, in, err)
		}
	}
}

func TestStoreRoutedAndUnrouted(t *testing.T) {
	s := openHeartTestStore(t)
	ctx := context.Background()

	ev := &Event{ID: "e1", Source: "x", Kind: "k", OccurredAt: "2030-01-01 00:00:00"}
	if _, err := s.Persist(ctx, ev); err != nil {
		t.Fatal(err)
	}
	pending, err := s.Unrouted(ctx)
	if err != nil || len(pending) != 1 || pending[0].ID != "e1" {
		t.Fatalf("unrouted = %#v err=%v", pending, err)
	}
	if err := s.MarkRouted(ctx, "e1", "routed"); err != nil {
		t.Fatal(err)
	}
	pending, _ = s.Unrouted(ctx)
	if len(pending) != 0 {
		t.Fatalf("unrouted after mark = %d, want 0", len(pending))
	}
	if err := s.MarkRouted(ctx, "missing", "routed"); err == nil {
		t.Fatal("MarkRouted(missing) error = nil, want not-found")
	}
}

func TestHeartProcessDeliversOnceAndDedups(t *testing.T) {
	s := openHeartTestStore(t)
	rec := &recorder{}
	h := New(s, rec.handle)
	ctx := context.Background()

	h.process(ctx, Event{Source: "mail", Kind: "mail.received", DedupKey: "m1"})
	h.process(ctx, Event{Source: "mail", Kind: "mail.received", DedupKey: "m1"}) // duplicate
	h.process(ctx, Event{Source: "mail", Kind: "mail.received", DedupKey: "m2"})

	if rec.count() != 2 {
		t.Fatalf("delivered = %d, want 2 (duplicate suppressed)", rec.count())
	}
	pending, _ := s.Unrouted(ctx)
	if len(pending) != 0 {
		t.Fatalf("unrouted = %d, want 0 (all routed)", len(pending))
	}
}

func TestHeartRecoverReplaysBacklog(t *testing.T) {
	s := openHeartTestStore(t)
	ctx := context.Background()
	// simulate a crash: events persisted but never routed
	for _, id := range []string{"a", "b"} {
		if _, err := s.Persist(ctx, &Event{ID: id, Source: "x", Kind: "k", OccurredAt: "2030-01-01 00:00:00"}); err != nil {
			t.Fatal(err)
		}
	}
	rec := &recorder{}
	h := New(s, rec.handle)
	h.recover(ctx)

	if rec.count() != 2 {
		t.Fatalf("recovered = %d, want 2", rec.count())
	}
	pending, _ := s.Unrouted(ctx)
	if len(pending) != 0 {
		t.Fatalf("unrouted after recover = %d, want 0", len(pending))
	}
}

func TestTimerSourceEmitsThenStops(t *testing.T) {
	s := openHeartTestStore(t)
	rec := &recorder{}
	h := New(s, rec.handle)
	h.Register(&TimerSource{SourceName: "timer", Kind: "tick", Interval: 5 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = h.Run(ctx); close(done) }()

	deadline := time.After(2 * time.Second)
	for rec.count() == 0 {
		select {
		case <-deadline:
			cancel()
			t.Fatal("timer source emitted no events within 2s")
		case <-time.After(2 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("heart did not stop after context cancel")
	}
	// emitted events carry the source name
	rec.mu.Lock()
	src := rec.events[0].Source
	rec.mu.Unlock()
	if src != "timer" {
		t.Fatalf("event source = %q, want timer", src)
	}
}
