package sleep

import (
	"context"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/world"
)

func seedFact(t *testing.T, ws *world.Store, id, summary string) {
	t.Helper()
	if err := ws.AppendJournal(context.Background(), world.JournalEntry{
		ID: id, Kind: "fact", Summary: summary,
	}); err != nil {
		t.Fatalf("seed fact %s: %v", id, err)
	}
}

func factIDSet(t *testing.T, ws *world.Store) map[string]bool {
	t.Helper()
	facts, err := ws.ListFacts(context.Background(), 100)
	if err != nil {
		t.Fatalf("ListFacts: %v", err)
	}
	m := map[string]bool{}
	for _, f := range facts {
		m[f.ID] = true
	}
	return m
}

func hasCorrectionNote(t *testing.T, ws *world.Store, wantSubstr string) bool {
	t.Helper()
	entries, err := ws.ListJournal(context.Background(), "", 200)
	if err != nil {
		t.Fatalf("ListJournal: %v", err)
	}
	for _, e := range entries {
		if e.Kind == "correction" && strings.Contains(e.Summary, wantSubstr) {
			return true
		}
	}
	return false
}

func TestReconcileSupersedesContradictingFact(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	seedFact(t, ws, "f-old", "user lives in Boston")
	seedFact(t, ws, "f-new", "user lives in Seattle")

	sum := &stubSummarizer{out: `{"reconcile":[{"canonical_id":"f-new","superseded_ids":["f-old"],"reason":"user moved; Boston is stale"}]}`}
	job := NewReconcileJob(ws, sum)

	msg, err := job.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "superseded 1 stale fact(s)" {
		t.Fatalf("unexpected summary: %q", msg)
	}

	ids := factIDSet(t, ws)
	if ids["f-old"] {
		t.Fatal("stale fact f-old should be removed from retrieval")
	}
	if !ids["f-new"] {
		t.Fatal("canonical fact f-new must survive")
	}
	// The judge must have seen both facts.
	if !strings.Contains(sum.gotInput, "f-old") || !strings.Contains(sum.gotInput, "f-new") {
		t.Fatalf("reconcile input missing facts:\n%s", sum.gotInput)
	}
	// A correction note must record the supersession for the audit trail.
	if !hasCorrectionNote(t, ws, "Fact f-old superseded by f-new") {
		t.Fatal("expected a correction journal note")
	}
	// SoftInvalidate: the superseded fact's content must survive in the append-only
	// correction trace, so a false positive does not destroy knowledge.
	if !correctionDetailContains(t, ws, "user lives in Boston") {
		t.Fatal("correction note must preserve the superseded fact's content")
	}
}

func correctionDetailContains(t *testing.T, ws *world.Store, want string) bool {
	t.Helper()
	entries, err := ws.ListJournal(context.Background(), "", 200)
	if err != nil {
		t.Fatalf("ListJournal: %v", err)
	}
	for _, e := range entries {
		if e.Kind == "correction" && strings.Contains(e.Detail, want) {
			return true
		}
	}
	return false
}

func TestReconcileIgnoresHallucinatedCanonicalAndKeptIDs(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	seedFact(t, ws, "f-a", "fact A")
	seedFact(t, ws, "f-b", "fact B")
	seedFact(t, ws, "f-c", "fact C")

	// The judge: group 1 keeps f-a, supersedes f-b plus a hallucinated id and f-a
	// itself (self) — only f-b is a legitimate deletion. Group 2 keeps f-c and tries
	// to supersede f-a, but f-a is canonical in group 1 so it must be protected.
	sum := &stubSummarizer{out: `{"reconcile":[
		{"canonical_id":"f-a","superseded_ids":["f-b","f-ghost","f-a"],"reason":"dup"},
		{"canonical_id":"f-c","superseded_ids":["f-a"],"reason":"conflict"}
	]}`}
	job := NewReconcileJob(ws, sum)

	msg, err := job.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "superseded 1 stale fact(s)" {
		t.Fatalf("expected exactly 1 supersession, got %q", msg)
	}
	ids := factIDSet(t, ws)
	if ids["f-b"] {
		t.Fatal("f-b should have been superseded")
	}
	if !ids["f-a"] {
		t.Fatal("f-a is canonical and must never be deleted")
	}
	if !ids["f-c"] {
		t.Fatal("f-c is canonical and must survive")
	}
}

func TestReconcileSkipsWhenTooFewFacts(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	seedFact(t, ws, "f-only", "the only fact")

	sum := &stubSummarizer{out: `{"reconcile":[{"canonical_id":"x","superseded_ids":["f-only"]}]}`}
	job := NewReconcileJob(ws, sum)

	msg, err := job.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "nothing to reconcile" {
		t.Fatalf("unexpected summary: %q", msg)
	}
	if sum.gotInput != "" {
		t.Fatal("summarizer must not be called with fewer than 2 facts")
	}
	if !factIDSet(t, ws)["f-only"] {
		t.Fatal("the lone fact must be untouched")
	}
}

func TestReconcileUnparseableVerdictIsNoOp(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	seedFact(t, ws, "f-1", "fact one")
	seedFact(t, ws, "f-2", "fact two")

	sum := &stubSummarizer{out: "the model rambled and produced no JSON object"}
	job := NewReconcileJob(ws, sum)

	msg, err := job.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "no contradictions found" {
		t.Fatalf("unexpected summary: %q", msg)
	}
	ids := factIDSet(t, ws)
	if !ids["f-1"] || !ids["f-2"] {
		t.Fatal("an unparseable verdict must not delete any fact")
	}
}

func TestReconcileEmptyReconcileIsNoOp(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	seedFact(t, ws, "f-1", "fact one")
	seedFact(t, ws, "f-2", "fact two")

	sum := &stubSummarizer{out: `{"reconcile":[]}`}
	job := NewReconcileJob(ws, sum)

	msg, err := job.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "no contradictions found" {
		t.Fatalf("unexpected summary: %q", msg)
	}
	if len(factIDSet(t, ws)) != 2 {
		t.Fatal("an empty reconcile list must leave all facts intact")
	}
}

func TestReconcileRequiresWorldAndSummarizer(t *testing.T) {
	if _, err := (&ReconcileJob{}).Run(context.Background()); err == nil {
		t.Fatal("expected error when world and summarizer are nil")
	}
}
