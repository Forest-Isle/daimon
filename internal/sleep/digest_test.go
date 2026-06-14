package sleep

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/world"
)

// stubSummarizer records the content it was asked to summarize and returns a
// fixed digest (or error), so the digest job is tested deterministically.
type stubSummarizer struct {
	out      string
	err      error
	gotInput string
}

func (s *stubSummarizer) Complete(_ context.Context, _, userMessage string) (string, error) {
	s.gotInput = userMessage
	return s.out, s.err
}

func openWorldStore(t *testing.T) *world.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "sleep.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return world.NewStore(db.DB)
}

func TestDigestJobWritesRetrievableFact(t *testing.T) {
	ws := openWorldStore(t)
	ctx := context.Background()
	if err := ws.AppendJournal(ctx, world.JournalEntry{ID: "j1", Kind: "decision", Summary: "chose SQLite for storage"}); err != nil {
		t.Fatal(err)
	}
	sum := &stubSummarizer{out: "You are maintaining a local-first agent. Recent focus: storage choices."}
	job := NewDigestJob(ws, sum)

	msg, err := job.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(msg, "regenerated") {
		t.Fatalf("unexpected job summary: %q", msg)
	}
	// The journal entry it summarized must have reached the summarizer input.
	if !strings.Contains(sum.gotInput, "chose SQLite for storage") {
		t.Fatalf("journal entry not fed to summarizer:\n%s", sum.gotInput)
	}

	// The digest must be persisted as a retrievable fact.
	hits, err := ws.Retrieve(ctx, world.Query{Text: "storage"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range hits {
		if h.ID == digestFactID && h.Kind == "fact" {
			found = true
		}
	}
	if !found {
		t.Fatalf("self-digest fact not retrievable: %+v", hits)
	}
}

func TestDigestJobReplacesPriorDigest(t *testing.T) {
	ws := openWorldStore(t)
	ctx := context.Background()
	if err := ws.AppendJournal(ctx, world.JournalEntry{ID: "j1", Kind: "outcome", Summary: "shipped feature"}); err != nil {
		t.Fatal(err)
	}
	job := NewDigestJob(ws, &stubSummarizer{out: "first digest"})
	if _, err := job.Run(ctx); err != nil {
		t.Fatal(err)
	}
	job2 := NewDigestJob(ws, &stubSummarizer{out: "second digest"})
	if _, err := job2.Run(ctx); err != nil {
		t.Fatal(err)
	}

	// Only one digest fact should exist (fact.upsert replaces by id).
	entries, err := ws.ListJournal(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	var detail string
	for _, e := range entries {
		if e.ID == digestFactID {
			count++
			detail = e.Detail
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 digest fact after replace, got %d", count)
	}
	if detail != "second digest" {
		t.Fatalf("digest not replaced with latest, got %q", detail)
	}
}

func TestDigestJobEmptyWorldSkips(t *testing.T) {
	ws := openWorldStore(t)
	sum := &stubSummarizer{out: "should not be called"}
	job := NewDigestJob(ws, sum)
	msg, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(msg, "nothing to digest") {
		t.Fatalf("empty world should skip, got %q", msg)
	}
	if sum.gotInput != "" {
		t.Fatal("summarizer should not be called for an empty world")
	}
}

func TestDigestJobSummarizerErrorPropagates(t *testing.T) {
	ws := openWorldStore(t)
	ctx := context.Background()
	if err := ws.AppendJournal(ctx, world.JournalEntry{ID: "j1", Kind: "outcome", Summary: "did a thing"}); err != nil {
		t.Fatal(err)
	}
	job := NewDigestJob(ws, &stubSummarizer{err: errors.New("llm down")})
	if _, err := job.Run(ctx); err == nil {
		t.Fatal("summarizer error should propagate")
	}
}
