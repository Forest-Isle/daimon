package economy

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/daimon/internal/store"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "economy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db.DB)
}

func TestRecordAssignsIDAndAggregates(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	e := Entry{EpisodeID: "ep1", Model: "claude", Provider: "claude", InputTokens: 100, OutputTokens: 40, CacheReadTokens: 25, OccurredAt: 1000}
	if err := s.Record(ctx, e); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := s.Record(ctx, Entry{EpisodeID: "ep2", Model: "claude", InputTokens: 50, OutputTokens: 10, OccurredAt: 2000}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	total, err := s.TotalSince(ctx, 0)
	if err != nil {
		t.Fatalf("TotalSince: %v", err)
	}
	if total.Episodes != 2 || total.InputTokens != 150 || total.OutputTokens != 50 || total.CacheReadTokens != 25 {
		t.Fatalf("totals = %+v", total)
	}

	// since cutoff excludes the older row.
	recent, err := s.TotalSince(ctx, 1500)
	if err != nil {
		t.Fatalf("TotalSince(1500): %v", err)
	}
	if recent.Episodes != 1 || recent.InputTokens != 50 {
		t.Fatalf("recent totals = %+v", recent)
	}
}

func TestRecordClampsNegativeTokens(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Record(ctx, Entry{EpisodeID: "ep", InputTokens: -5, OutputTokens: -1, CacheReadTokens: -9, OccurredAt: 1}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	total, err := s.TotalSince(ctx, 0)
	if err != nil {
		t.Fatalf("TotalSince: %v", err)
	}
	if total.InputTokens != 0 || total.OutputTokens != 0 || total.CacheReadTokens != 0 {
		t.Fatalf("negative tokens must clamp to zero, got %+v", total)
	}
}

func TestRecordIsIdempotentPerEpisode(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	// Re-recording the same episode (e.g. a retried/concurrent re-delivery) must
	// not double-count: the deterministic id + INSERT OR IGNORE collapses to one row,
	// and the FIRST write's token counts win (the second is ignored entirely).
	if err := s.Record(ctx, Entry{EpisodeID: "ep", InputTokens: 100, OccurredAt: 1}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := s.Record(ctx, Entry{EpisodeID: "ep", InputTokens: 999, OccurredAt: 2}); err != nil {
		t.Fatalf("Record (dup): %v", err)
	}
	total, _ := s.TotalSince(ctx, 0)
	if total.Episodes != 1 || total.InputTokens != 100 {
		t.Fatalf("same episode must record exactly one row (first wins), got %+v", total)
	}
}

func TestRecordDistinctEpisodesAreDistinctRows(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		if err := s.Record(ctx, Entry{EpisodeID: id, InputTokens: 1, OccurredAt: 1}); err != nil {
			t.Fatalf("Record %s: %v", id, err)
		}
	}
	if total, _ := s.TotalSince(ctx, 0); total.Episodes != 3 {
		t.Fatalf("want 3 distinct rows, got %d", total.Episodes)
	}
	// An episode with no id cannot dedup — each gets a random id.
	for i := 0; i < 2; i++ {
		if err := s.Record(ctx, Entry{InputTokens: 1, OccurredAt: 1}); err != nil {
			t.Fatalf("Record empty-id: %v", err)
		}
	}
	if total, _ := s.TotalSince(ctx, 0); total.Episodes != 5 {
		t.Fatalf("empty-id rows must not dedup, got %d", total.Episodes)
	}
}

func TestNilStore(t *testing.T) {
	var s *Store
	if err := s.Record(context.Background(), Entry{}); err == nil {
		t.Fatal("nil store Record must error")
	}
	if _, err := s.TotalSince(context.Background(), 0); err == nil {
		t.Fatal("nil store TotalSince must error")
	}
}
