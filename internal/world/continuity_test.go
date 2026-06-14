package world

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/store"
)

// TestWorldContinuityAcrossRestart is the "world is the sole source of truth"
// acceptance gate (DAIMON_BLUEPRINT.md §4.3): after killing all runtime state and
// restarting from the ~/.daimon directory alone, the agent must still answer "who
// am I", "what are we doing", and "what happened last week" consistently. It
// writes that state, closes the store and identity, then reopens fresh handles
// over the SAME files and asserts each question is answerable purely from disk.
//
// It guards the guarantee before the legacy in-process memory tooling is retired
// (CF3): if retiring that tooling ever regresses durable recall, this fails.
func TestWorldContinuityAcrossRestart(t *testing.T) {
	ctx := context.Background()
	appDir := t.TempDir()
	dbPath := filepath.Join(appDir, "world.db")
	identityDir := filepath.Join(appDir, "identity")

	// ---- First "boot": persist the three-question state, then shut down. ----
	identity := Identity{Dir: identityDir}
	if err := identity.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	const digest = "name: Daimon\nrole: LO's personal agent\nfocus: re-founding the world model\n"
	if err := os.WriteFile(filepath.Join(identityDir, "digest.md"), []byte(digest), 0o644); err != nil {
		t.Fatalf("write identity digest: %v", err)
	}

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	w := NewStore(db.DB)

	if err := w.CreateCommitment(ctx, Commitment{ID: "commit_blueprint", Kind: "project", Title: "Ship the world model", State: "active"}); err != nil {
		t.Fatalf("create commitment 1: %v", err)
	}
	if err := w.CreateCommitment(ctx, Commitment{ID: "commit_review", Kind: "promise", Title: "Review the daimon roadmap weekly", State: "active"}); err != nil {
		t.Fatalf("create commitment 2: %v", err)
	}
	// "Last week" activity: a dated decision the restart must still surface.
	if err := w.AppendJournal(ctx, JournalEntry{
		ID: "journal_decision_sqlite", Kind: "decision",
		Summary: "Chose SQLite as the single durable substrate", OccurredAt: "2026-06-07T09:00:00Z",
	}); err != nil {
		t.Fatalf("append journal: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// ---- "Restart": brand-new handles over the same files, no shared state. ----
	db2, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = db2.Close() }()
	w2 := NewStore(db2.DB)
	identity2 := Identity{Dir: identityDir}

	// Q1 "who am I" — identity digest reconstructed from disk.
	if got := identity2.Digest(); got != digest {
		t.Fatalf("identity digest not recovered after restart:\n got=%q\nwant=%q", got, digest)
	}

	// Q2 "what are we doing" — active commitments reconstructed from disk.
	active, err := w2.ListCommitments(ctx, []string{"active"}, "")
	if err != nil {
		t.Fatalf("ListCommitments: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("active commitments after restart = %d, want 2: %#v", len(active), active)
	}
	commitDigest, err := w2.CommitmentsDigest(ctx, "")
	if err != nil {
		t.Fatalf("CommitmentsDigest: %v", err)
	}
	if !strings.Contains(commitDigest, "Ship the world model") || !strings.Contains(commitDigest, "Review the daimon roadmap weekly") {
		t.Fatalf("commitments digest missing entries after restart:\n%s", commitDigest)
	}

	// Q3 "what happened last week" — the dated decision is retrievable from disk.
	hits, err := w2.Retrieve(ctx, Query{Text: "SQLite substrate"})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	found := false
	for _, h := range hits {
		if h.ID == "journal_decision_sqlite" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("last-week decision not retrievable after restart: %#v", hits)
	}
}
