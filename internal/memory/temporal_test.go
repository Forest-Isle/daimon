package memory

import (
	"context"
	"testing"
	"time"
)

func TestSoftInvalidate(t *testing.T) {
	store, cleanup := setupTestFileStore(t)
	defer cleanup()

	ctx := context.Background()
	id := "temporal_test_001"
	now := time.Now().UTC()

	// Save a fact.
	err := store.Save(ctx, Entry{
		ID:        id,
		Scope:     ScopeUser,
		Content:   "Alice prefers Python for data science.",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify it's findable via search (valid_to is NULL by default).
	results, err := store.Search(ctx, SearchQuery{Text: "Alice prefers Python"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected fact to be searchable before invalidation")
	}

	// Soft-invalidate the fact.
	err = store.SoftInvalidate(ctx, id)
	if err != nil {
		t.Fatalf("SoftInvalidate: %v", err)
	}

	// Verify it's no longer findable via default search (excludes invalidated facts).
	results, err = store.Search(ctx, SearchQuery{Text: "Alice prefers Python"})
	if err != nil {
		t.Fatalf("Search after invalidation: %v", err)
	}
	if len(results) > 0 {
		t.Fatalf("expected 0 results after soft invalidation, got %d", len(results))
	}

	// But it should be findable with IncludeHistorical=true.
	results, err = store.Search(ctx, SearchQuery{
		Text:              "Alice prefers Python",
		IncludeHistorical: true,
	})
	if err != nil {
		t.Fatalf("Historical search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected fact to be findable with IncludeHistorical=true")
	}

	// Double invalidation should fail.
	err = store.SoftInvalidate(ctx, id)
	if err == nil {
		t.Fatal("expected error on double invalidation")
	}
}

func TestTemporalFact_NewFactHasValidFrom(t *testing.T) {
	store, cleanup := setupTestFileStore(t)
	defer cleanup()

	ctx := context.Background()
	id := "temporal_test_002"
	now := time.Now().UTC()

	err := store.Save(ctx, Entry{
		ID:        id,
		Scope:     ScopeUser,
		Content:   "Bob uses Go for backend services.",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Search should find it (valid_to IS NULL, valid_from is set).
	results, err := store.Search(ctx, SearchQuery{Text: "Bob uses Go"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected new fact to be searchable")
	}

	// Verify it appears in historical search too.
	results, err = store.Search(ctx, SearchQuery{
		Text:              "Bob uses Go",
		IncludeHistorical: true,
	})
	if err != nil {
		t.Fatalf("Historical search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected new fact in historical search")
	}
}

func TestTemporalFact_ContradictionPattern(t *testing.T) {
	store, cleanup := setupTestFileStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()
	oldID := "temporal_test_003"
	newID := "temporal_test_004"

	// Save original fact.
	err := store.Save(ctx, Entry{
		ID:        oldID,
		Scope:     ScopeUser,
		Content:   "Carol lives in San Francisco.",
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  map[string]string{"type": "semantic"},
	})
	if err != nil {
		t.Fatalf("Save old: %v", err)
	}

	// Later: Carol moved. Invalidate old fact, save new one.
	err = store.SoftInvalidate(ctx, oldID)
	if err != nil {
		t.Fatalf("SoftInvalidate old: %v", err)
	}

	later := now.Add(24 * time.Hour)
	err = store.Save(ctx, Entry{
		ID:        newID,
		Scope:     ScopeUser,
		Content:   "Carol lives in New York.",
		CreatedAt: later,
		UpdatedAt: later,
		Metadata:  map[string]string{"type": "semantic"},
	})
	if err != nil {
		t.Fatalf("Save new: %v", err)
	}

	// Default search should only return the new fact.
	results, err := store.Search(ctx, SearchQuery{Text: "Carol lives"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 current fact, got %d", len(results))
	}
	if results[0].Entry.ID != newID {
		t.Errorf("expected new fact %s, got %s", newID, results[0].Entry.ID)
	}

	// Historical search should return both.
	results, err = store.Search(ctx, SearchQuery{
		Text:              "Carol lives",
		IncludeHistorical: true,
	})
	if err != nil {
		t.Fatalf("Historical search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 historical facts, got %d", len(results))
	}
}
