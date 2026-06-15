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
	if _, err := s.ByModelSince(context.Background(), 0); err == nil {
		t.Fatal("nil store ByModelSince must error")
	}
}

func TestByModelSince(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	// Two models, opus the heavier output spender; one row predates the cutoff.
	must := func(e Entry) {
		if err := s.Record(ctx, e); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	must(Entry{EpisodeID: "a", Model: "opus", InputTokens: 100, OutputTokens: 200, OccurredAt: 2000})
	must(Entry{EpisodeID: "b", Model: "opus", InputTokens: 10, OutputTokens: 20, OccurredAt: 2500})
	must(Entry{EpisodeID: "c", Model: "haiku", InputTokens: 5, OutputTokens: 5, OccurredAt: 2500})
	must(Entry{EpisodeID: "old", Model: "opus", InputTokens: 999, OutputTokens: 999, OccurredAt: 1000})

	rows, err := s.ByModelSince(ctx, 1500)
	if err != nil {
		t.Fatalf("ByModelSince: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 model groups, got %d: %+v", len(rows), rows)
	}
	// Ordered by output desc: opus (220) before haiku (5). Cutoff excludes the old row.
	if rows[0].Model != "opus" || rows[0].Episodes != 2 || rows[0].InputTokens != 110 || rows[0].OutputTokens != 220 {
		t.Fatalf("opus group wrong: %+v", rows[0])
	}
	if rows[1].Model != "haiku" || rows[1].Episodes != 1 || rows[1].OutputTokens != 5 {
		t.Fatalf("haiku group wrong: %+v", rows[1])
	}
}

func TestByClassModelSince(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	must := func(e Entry) {
		if err := s.Record(ctx, e); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	// chat spans two models (opus + haiku); one heartbeat; one unclassified (empty
	// class); one chat predates cutoff.
	must(Entry{EpisodeID: "c1", ActivityClass: "chat", Model: "opus", OutputTokens: 100, OccurredAt: 2000})
	must(Entry{EpisodeID: "c2", ActivityClass: "chat", Model: "haiku", OutputTokens: 50, OccurredAt: 2500})
	must(Entry{EpisodeID: "h1", ActivityClass: "internal.heartbeat", Model: "opus", OutputTokens: 200, OccurredAt: 2500})
	must(Entry{EpisodeID: "u1", ActivityClass: "", Model: "opus", OutputTokens: 5, OccurredAt: 2500})
	must(Entry{EpisodeID: "old", ActivityClass: "chat", Model: "opus", OutputTokens: 999, OccurredAt: 1000})

	rows, err := s.ByClassModelSince(ctx, 1500)
	if err != nil {
		t.Fatalf("ByClassModelSince: %v", err)
	}
	// (class,model) groups, ordered class ASC then model ASC; empty class sorts
	// first; old chat/opus excluded by cutoff so chat/opus = 1 episode / 100 output.
	want := []ClassModelTotals{
		{Class: "", Model: "opus", Totals: Totals{Episodes: 1, OutputTokens: 5}},
		{Class: "chat", Model: "haiku", Totals: Totals{Episodes: 1, OutputTokens: 50}},
		{Class: "chat", Model: "opus", Totals: Totals{Episodes: 1, OutputTokens: 100}},
		{Class: "internal.heartbeat", Model: "opus", Totals: Totals{Episodes: 1, OutputTokens: 200}},
	}
	if len(rows) != len(want) {
		t.Fatalf("want %d rows, got %d: %+v", len(want), len(rows), rows)
	}
	for i, w := range want {
		if rows[i].Class != w.Class || rows[i].Model != w.Model || rows[i].Episodes != w.Episodes || rows[i].OutputTokens != w.OutputTokens {
			t.Fatalf("row %d = %+v, want %+v", i, rows[i], w)
		}
	}

	if _, err := (*Store)(nil).ByClassModelSince(ctx, 0); err == nil {
		t.Fatal("nil store ByClassModelSince must error")
	}
}

func TestPricesCostUSD(t *testing.T) {
	prices := Prices{
		"claude-opus": {InputPerMTok: 15, OutputPerMTok: 75, CacheReadPerMTok: 1.5, CacheCreationPerMTok: 18.75},
	}
	// 1M input @15 + 1M output @75 + 1M cache-read @1.5 + 1M cache-create @18.75 = 110.25.
	t1 := Totals{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000, CacheCreationTokens: 1_000_000}
	// Longest-substring match: "claude-opus" key prices the full "claude-opus-4-8" model id.
	usd, priced := prices.CostUSD("claude-opus-4-8", t1)
	if !priced {
		t.Fatal("expected priced=true via substring match")
	}
	if usd < 110.24 || usd > 110.26 {
		t.Fatalf("want ~110.25, got %v", usd)
	}
	// Unknown model: tokens only, never a misleading $0.
	if _, priced := prices.CostUSD("gpt-4", t1); priced {
		t.Fatal("unknown model must report priced=false")
	}
}

func TestPricesLookupPrefersExactThenLongest(t *testing.T) {
	prices := Prices{
		"claude":          {OutputPerMTok: 1},
		"claude-opus":     {OutputPerMTok: 2},
		"claude-opus-4-8": {OutputPerMTok: 3},
	}
	out := Totals{OutputTokens: 1_000_000}
	// Exact key wins.
	if usd, _ := prices.CostUSD("claude-opus-4-8", out); usd != 3 {
		t.Fatalf("exact match want 3, got %v", usd)
	}
	// No exact key ⇒ longest containing substring ("claude-opus" beats "claude").
	if usd, _ := prices.CostUSD("claude-opus-4-9", out); usd != 2 {
		t.Fatalf("longest-substring want 2, got %v", usd)
	}
	// Empty-string key must never match (substring path)…
	if _, priced := (Prices{"": {OutputPerMTok: 9}}).CostUSD("anything", out); priced {
		t.Fatal("empty key must not match")
	}
	// …nor exact-match an empty model id (the report's "(unknown)" rows).
	if _, priced := (Prices{"": {OutputPerMTok: 9}}).CostUSD("", out); priced {
		t.Fatal("empty model must not be priced by an empty key")
	}
}

func TestPricesTieBreakIsDeterministic(t *testing.T) {
	// Two equal-length keys both contained in the model id. The lexicographically
	// smaller key ("aa") must win every time, independent of map iteration order.
	prices := Prices{
		"aa": {OutputPerMTok: 1},
		"bb": {OutputPerMTok: 2},
	}
	out := Totals{OutputTokens: 1_000_000}
	for i := 0; i < 50; i++ {
		usd, priced := prices.CostUSD("xaabb", out)
		if !priced || usd != 1 {
			t.Fatalf("tie-break not deterministic: usd=%v priced=%v", usd, priced)
		}
	}
}

func TestTotalSinceIncludesNonPositiveTimestamps(t *testing.T) {
	// since<=0 means "all rows" — including rows whose occurred_at is itself <= 0,
	// which a bare `occurred_at >= since` with since=0 would silently drop.
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Record(ctx, Entry{EpisodeID: "z", InputTokens: 7, OccurredAt: 0}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	total, err := s.TotalSince(ctx, 0)
	if err != nil {
		t.Fatalf("TotalSince: %v", err)
	}
	if total.Episodes != 1 || total.InputTokens != 7 {
		t.Fatalf("since<=0 must include occurred_at=0 row, got %+v", total)
	}
}
