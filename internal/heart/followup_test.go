package heart

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/store"
)

func openFollowUpStore(t *testing.T) *FollowUpStore {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "followup.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewFollowUpStore(db.DB)
}

func TestFollowUpStoreDueAndMarkFired(t *testing.T) {
	s := openFollowUpStore(t)
	ctx := context.Background()

	// One due (past), one not yet due (future).
	if err := s.Create(ctx, FollowUp{ID: "due", Goal: "resume", FireAt: 100}); err != nil {
		t.Fatalf("Create(due) error = %v", err)
	}
	if err := s.Create(ctx, FollowUp{ID: "later", Goal: "wait", FireAt: 10_000}); err != nil {
		t.Fatalf("Create(later) error = %v", err)
	}

	due, err := s.Due(ctx, 200)
	if err != nil {
		t.Fatalf("Due() error = %v", err)
	}
	if len(due) != 1 || due[0].ID != "due" || due[0].Goal != "resume" {
		t.Fatalf("due = %#v, want only 'due'", due)
	}

	if err := s.MarkFired(ctx, "due"); err != nil {
		t.Fatalf("MarkFired() error = %v", err)
	}
	due, _ = s.Due(ctx, 200)
	if len(due) != 0 {
		t.Fatalf("due after fired = %d, want 0", len(due))
	}
	if err := s.MarkFired(ctx, "missing"); err == nil {
		t.Fatal("MarkFired(missing) error = nil, want not-found")
	}
}

func TestFollowUpSourceEmitsDueAndDedups(t *testing.T) {
	s := openFollowUpStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, FollowUp{ID: "f1", Goal: "do the thing", FireAt: 1}); err != nil {
		t.Fatal(err)
	}

	src := &FollowUpSource{Store: s, Interval: 2 * time.Millisecond, Now: func() time.Time { return time.Unix(1000, 0) }}

	// got is written by the source goroutine (via emit) and read by this
	// goroutine while polling, so it must be mutex-guarded.
	var mu sync.Mutex
	var got []Event
	emit := func(ev Event) { mu.Lock(); got = append(got, ev); mu.Unlock() }
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(got) }

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = src.Run(runCtx, emit); close(done) }()

	deadline := time.After(2 * time.Second)
	for count() == 0 {
		select {
		case <-deadline:
			cancel()
			t.Fatal("follow-up source emitted nothing within 2s")
		case <-time.After(2 * time.Millisecond):
		}
	}
	cancel()
	<-done // source goroutine has exited; no more writes to got

	if got[0].Kind != "internal.followup" || got[0].Payload != "do the thing" {
		t.Fatalf("event = %#v", got[0])
	}
	if got[0].DedupKey != "followup:f1" {
		t.Fatalf("dedup key = %q, want followup:f1", got[0].DedupKey)
	}
	// After firing, no further pending entries remain.
	due, _ := s.Due(ctx, 2000)
	if len(due) != 0 {
		t.Fatalf("due after source ran = %d, want 0", len(due))
	}
}
