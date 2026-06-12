package memory

import (
	"context"
	"testing"
	"time"
)

type countingStore struct {
	searches int
}

func (s *countingStore) Save(context.Context, Entry) error { return nil }

func (s *countingStore) Search(_ context.Context, q SearchQuery) ([]SearchResult, error) {
	s.searches++
	return []SearchResult{{
		Entry: Entry{
			ID:      q.Text,
			Content: q.TypeFilter,
		},
		Score: float64(s.searches),
	}}, nil
}

func (s *countingStore) ListByScope(context.Context, MemoryScope, string) ([]Entry, error) {
	return nil, nil
}

func (s *countingStore) Update(context.Context, string, string, int) error { return nil }

func (s *countingStore) Delete(context.Context, string) error { return nil }

func TestCachedStoreSearchKeyIncludesFilters(t *testing.T) {
	inner := &countingStore{}
	store := NewCachedStore(inner, 10, time.Minute)
	ctx := context.Background()

	base := SearchQuery{Text: "deploy", Limit: 5, UserID: "u1", TypeFilter: "fact"}
	if _, err := store.Search(ctx, base); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Search(ctx, base); err != nil {
		t.Fatal(err)
	}
	if inner.searches != 1 {
		t.Fatalf("expected cached identical query to hit inner once, got %d", inner.searches)
	}

	changed := base
	changed.TypeFilter = "decision"
	if _, err := store.Search(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if inner.searches != 2 {
		t.Fatalf("expected changed filter to miss cache, got %d inner calls", inner.searches)
	}
}

func TestCachedStoreSearchKeyIncludesEmbedding(t *testing.T) {
	inner := &countingStore{}
	store := NewCachedStore(inner, 10, time.Minute)
	ctx := context.Background()

	q1 := SearchQuery{Text: "same", Embedding: []float32{1, 2, 3}, Limit: 5}
	q2 := SearchQuery{Text: "same", Embedding: []float32{1, 2, 4}, Limit: 5}
	if _, err := store.Search(ctx, q1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Search(ctx, q2); err != nil {
		t.Fatal(err)
	}
	if inner.searches != 2 {
		t.Fatalf("expected changed embedding to miss cache, got %d inner calls", inner.searches)
	}
}
