package sleep

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/world"
)

// seedJournal appends n regular entries with deterministic, ordered timestamps.
func seedJournal(t *testing.T, ws *world.Store, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		err := ws.AppendJournal(ctx, world.JournalEntry{
			ID:         fmt.Sprintf("j%03d", i),
			Kind:       "outcome",
			Summary:    fmt.Sprintf("did thing %d", i),
			OccurredAt: fmt.Sprintf("2030-01-01 00:%02d:00", i),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRollupFoldsOlderEntries(t *testing.T) {
	ws := openWorldStore(t)
	ctx := context.Background()
	seedJournal(t, ws, 6) // j000(oldest) .. j005(newest)

	sum := &stubSummarizer{out: "Rollup: did several things in this period."}
	job := &RollupJob{world: ws, summarizer: sum, keepRecent: 2} // keep 2 newest, fold 4 oldest

	msg, err := job.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "rolled up 4 journal entries" {
		t.Fatalf("unexpected summary: %q", msg)
	}
	// Oldest entries must be fed to the summarizer, in chronological order.
	if !strings.Contains(sum.gotInput, "did thing 0") || !strings.Contains(sum.gotInput, "did thing 3") {
		t.Fatalf("foldable entries not summarized:\n%s", sum.gotInput)
	}
	if strings.Contains(sum.gotInput, "did thing 4") {
		t.Fatal("the recent window (keepRecent) must not be folded")
	}

	// A rollup entry now exists, and the 4 folded entries are tagged (so a second
	// pass folds nothing new).
	entries, err := ws.ListJournal(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	rollups, tagged := 0, 0
	for _, e := range entries {
		if e.Kind == "rollup" {
			rollups++
		}
		if e.RollupID != "" {
			tagged++
		}
	}
	if rollups != 1 {
		t.Fatalf("expected 1 rollup entry, got %d", rollups)
	}
	if tagged != 4 {
		t.Fatalf("expected 4 tagged (folded) entries, got %d", tagged)
	}

	// Idempotent: a second pass has nothing left to fold (only 2 unrolled remain).
	msg2, err := job.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if msg2 != "nothing to roll up" {
		t.Fatalf("second pass should find nothing, got %q", msg2)
	}
}

func TestRollupSkipsWhenTooFew(t *testing.T) {
	ws := openWorldStore(t)
	seedJournal(t, ws, 3)
	sum := &stubSummarizer{out: "should not be called"}
	job := &RollupJob{world: ws, summarizer: sum, keepRecent: 2} // only 1 foldable < rollupMinFold

	msg, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "nothing to roll up" {
		t.Fatalf("expected skip, got %q", msg)
	}
	if sum.gotInput != "" {
		t.Fatal("summarizer must not be called when there is too little to fold")
	}
}

func TestRollupNeverFoldsFacts(t *testing.T) {
	ws := openWorldStore(t)
	ctx := context.Background()
	// A fact (upserted singleton like the self-digest) must never be folded.
	if err := ws.AppendJournal(ctx, world.JournalEntry{ID: "fact1", Kind: "fact", Summary: "self digest", OccurredAt: "2030-01-01 00:00:00"}); err != nil {
		t.Fatal(err)
	}
	seedJournal(t, ws, 5) // 5 regular outcomes, newer

	sum := &stubSummarizer{out: "rollup summary"}
	job := &RollupJob{world: ws, summarizer: sum, keepRecent: 0} // fold all foldable regulars

	if _, err := job.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(sum.gotInput, "self digest") {
		t.Fatal("a fact must never be folded into a rollup")
	}
}
