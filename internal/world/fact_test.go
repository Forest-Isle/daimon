package world

import (
	"context"
	"testing"
)

// TestFactUpsert_AppendAndRetrieve verifies a fact.upsert mutation lands a
// kind=fact journal row that is immediately retrievable via FTS (trigger sync).
func TestFactUpsert_AppendAndRetrieve(t *testing.T) {
	db := openWorldTestDB(t)
	s := NewStore(db.DB)
	ctx := context.Background()

	err := s.Apply(ctx, "ep1", []Mutation{{
		Op:   "fact.upsert",
		Body: mustJSON(t, JournalEntry{ID: "fact_tz", Summary: "user is in Asia/Shanghai timezone"}),
	}})
	if err != nil {
		t.Fatalf("Apply fact.upsert: %v", err)
	}

	hits, err := s.Retrieve(ctx, Query{Text: "timezone"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "fact_tz" || hits[0].Kind != "fact" {
		t.Fatalf("fact not retrievable as kind=fact: %+v", hits)
	}
}

// TestFactUpsert_ReplaceByID verifies that re-upserting the same id replaces the
// fact in place and leaves no stale FTS row (delete-then-insert, not REPLACE).
func TestFactUpsert_ReplaceByID(t *testing.T) {
	db := openWorldTestDB(t)
	s := NewStore(db.DB)
	ctx := context.Background()

	for _, summary := range []string{"user prefers tabs", "user prefers spaces"} {
		if err := s.Apply(ctx, "ep1", []Mutation{{
			Op:   "fact.upsert",
			Body: mustJSON(t, JournalEntry{ID: "fact_indent", Summary: summary}),
		}}); err != nil {
			t.Fatalf("Apply fact.upsert(%q): %v", summary, err)
		}
	}

	// Only one journal row should exist for the id.
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM journal WHERE id = 'fact_indent'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 journal row after replace, got %d", count)
	}

	// The stale value must not be in FTS; the new value must be.
	stale, _ := s.Retrieve(ctx, Query{Text: "tabs"})
	if len(stale) != 0 {
		t.Fatalf("stale FTS row for replaced fact: %+v", stale)
	}
	fresh, _ := s.Retrieve(ctx, Query{Text: "spaces"})
	if len(fresh) != 1 || fresh[0].ID != "fact_indent" {
		t.Fatalf("replaced fact not retrievable: %+v", fresh)
	}
}

// TestFactUpsert_RequiresSummary rejects an empty fact.
func TestFactUpsert_RequiresSummary(t *testing.T) {
	db := openWorldTestDB(t)
	s := NewStore(db.DB)
	err := s.Apply(context.Background(), "ep1", []Mutation{{
		Op:   "fact.upsert",
		Body: mustJSON(t, JournalEntry{ID: "fact_empty"}),
	}})
	if err == nil {
		t.Fatal("fact.upsert with no summary should error")
	}
}

// TestFactUpsert_ForcesFactKind ignores a caller-supplied kind.
func TestFactUpsert_ForcesFactKind(t *testing.T) {
	db := openWorldTestDB(t)
	s := NewStore(db.DB)
	ctx := context.Background()
	if err := s.Apply(ctx, "ep1", []Mutation{{
		Op:   "fact.upsert",
		Body: mustJSON(t, JournalEntry{ID: "f1", Kind: "outcome", Summary: "the sky is blue"}),
	}}); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err := s.db.QueryRowContext(ctx, `SELECT kind FROM journal WHERE id = 'f1'`).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "fact" {
		t.Fatalf("fact.upsert kind = %q, want fact", kind)
	}
}
