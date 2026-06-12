package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/store"
)

func TestSearchQuery_ExcludeTypes(t *testing.T) {
	q := SearchQuery{
		Text:         "test",
		ExcludeTypes: []string{"profile"},
	}
	if len(q.ExcludeTypes) != 1 || q.ExcludeTypes[0] != "profile" {
		t.Fatalf("ExcludeTypes not set correctly: %v", q.ExcludeTypes)
	}
}

func TestExcludeTypes_FiltersProfileFromSearch(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	memDir := filepath.Join(tmpDir, "memory")
	fs, err := NewFileMemoryStore(memDir, db.DB, &NoopEmbedding{}, MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	now := time.Now()

	regular := Entry{
		ID: "mem_golang", Scope: ScopeUser, UserID: "u1",
		Content: "golang programming tips and tricks", CreatedAt: now, UpdatedAt: now,
	}
	profile := Entry{
		ID: "profile_comm", Scope: ScopeUser, UserID: "u1",
		Content: "golang concise communication style", CreatedAt: now, UpdatedAt: now,
		Metadata: map[string]string{"type": "profile", "section": "communication"},
	}

	if err := fs.Save(ctx, regular); err != nil {
		t.Fatalf("save regular: %v", err)
	}
	if err := fs.Save(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}

	all, err := fs.Search(ctx, SearchQuery{Text: "golang", Limit: 10, UserID: "u1"})
	if err != nil {
		t.Fatalf("search all: %v", err)
	}
	if len(all) < 2 {
		t.Fatalf("expected at least 2 results without ExcludeTypes, got %d", len(all))
	}

	hasProfile := false
	for _, r := range all {
		if r.Entry.ID == "profile_comm" {
			hasProfile = true
		}
	}
	if !hasProfile {
		t.Fatal("unfiltered search should include profile entry")
	}

	filtered, err := fs.Search(ctx, SearchQuery{
		Text: "golang", Limit: 10, UserID: "u1",
		ExcludeTypes: []string{"profile"},
	})
	if err != nil {
		t.Fatalf("search with ExcludeTypes: %v", err)
	}

	for _, r := range filtered {
		if r.Entry.ID == "profile_comm" {
			t.Error("ExcludeTypes should filter out profile entries")
		}
	}
	if len(filtered) >= len(all) {
		t.Errorf("filtered results (%d) should be fewer than unfiltered (%d)", len(filtered), len(all))
	}
}
