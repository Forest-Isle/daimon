package replay

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/daimon/internal/store"
)

func openCorrectionTestStore(t *testing.T) *CorrectionStore {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "corrections.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewCorrectionStore(db.DB)
}

func TestCorrectionStoreMarkListRoundTripAndOrder(t *testing.T) {
	s := openCorrectionTestStore(t)
	ctx := context.Background()

	if err := s.Mark(ctx, "later", "second", 20); err != nil {
		t.Fatalf("Mark(later): %v", err)
	}
	if err := s.Mark(ctx, "earlier", "first", 10); err != nil {
		t.Fatalf("Mark(earlier): %v", err)
	}

	got, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(List) = %d, want 2: %#v", len(got), got)
	}
	if got[0] != (Correction{SessionID: "earlier", Note: "first", CreatedAt: 10}) {
		t.Fatalf("first correction = %#v, want earlier/first/10", got[0])
	}
	if got[1] != (Correction{SessionID: "later", Note: "second", CreatedAt: 20}) {
		t.Fatalf("second correction = %#v, want later/second/20", got[1])
	}
}

func TestCorrectionStoreRejectsBlankSessionID(t *testing.T) {
	s := openCorrectionTestStore(t)
	if err := s.Mark(context.Background(), " \t\n ", "note", 1); err == nil {
		t.Fatal("Mark(blank) error = nil, want error")
	}
}

func TestCorrectionStoreMarkUpserts(t *testing.T) {
	s := openCorrectionTestStore(t)
	ctx := context.Background()

	if err := s.Mark(ctx, "session-1", "old", 100); err != nil {
		t.Fatalf("Mark(old): %v", err)
	}
	if err := s.Mark(ctx, "session-1", "new", 200); err != nil {
		t.Fatalf("Mark(new): %v", err)
	}

	got, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(List) = %d, want 1: %#v", len(got), got)
	}
	if got[0] != (Correction{SessionID: "session-1", Note: "new", CreatedAt: 200}) {
		t.Fatalf("upserted correction = %#v, want new note/time", got[0])
	}
}

func TestCorrectionStoreSessionIDSet(t *testing.T) {
	s := openCorrectionTestStore(t)
	ctx := context.Background()

	for _, id := range []string{"a", "b"} {
		if err := s.Mark(ctx, id, "", 1); err != nil {
			t.Fatalf("Mark(%s): %v", id, err)
		}
	}

	got, err := s.SessionIDSet(ctx)
	if err != nil {
		t.Fatalf("SessionIDSet: %v", err)
	}
	if len(got) != 2 || !got["a"] || !got["b"] {
		t.Fatalf("SessionIDSet = %#v, want a+b", got)
	}
}
