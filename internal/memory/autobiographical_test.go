package memory

import (
	"context"
	"strings"
	"testing"
)

type autobiographicalTestStore struct {
	saved []Entry
}

func (s *autobiographicalTestStore) Save(_ context.Context, entry Entry) error {
	s.saved = append(s.saved, entry)
	return nil
}

func (s *autobiographicalTestStore) Search(context.Context, SearchQuery) ([]SearchResult, error) {
	return nil, nil
}

func (s *autobiographicalTestStore) ListByScope(context.Context, MemoryScope, string) ([]Entry, error) {
	return nil, nil
}

func (s *autobiographicalTestStore) Update(context.Context, string, string, int) error { return nil }
func (s *autobiographicalTestStore) Delete(context.Context, string) error              { return nil }

func TestAutobiographicalStoreRecordDecision(t *testing.T) {
	store := &autobiographicalTestStore{}
	recorder := NewAutobiographicalStore(store, nil)

	id, err := recorder.RecordDecision(context.Background(), DecisionRecord{
		Decision: "keep core loop and structure runtime around it",
		Reason:   "the loop is the agent consciousness stream while policy and memory live in runtime layers",
		Context:  "super agent runtime architecture",
		Outcome:  "implemented prompt/tool/permission slices",
		Tags:     []string{"architecture", "runtime"},
	}, "sess_1", "user_1")
	if err != nil {
		t.Fatalf("RecordDecision() error = %v", err)
	}
	if id == "" {
		t.Fatal("expected decision id")
	}
	if len(store.saved) != 1 {
		t.Fatalf("saved entries = %d, want 1", len(store.saved))
	}
	entry := store.saved[0]
	if entry.Metadata["type"] != string(Autobiographical) {
		t.Fatalf("type metadata = %q", entry.Metadata["type"])
	}
	if entry.Metadata["category"] != "decision" {
		t.Fatalf("category metadata = %q", entry.Metadata["category"])
	}
	if !strings.Contains(entry.Content, "Decision: keep core loop") {
		t.Fatalf("content missing decision: %s", entry.Content)
	}
	if !strings.Contains(entry.Content, "Reason:") {
		t.Fatalf("content missing reason: %s", entry.Content)
	}
}

func TestAutobiographicalStoreRequiresDecision(t *testing.T) {
	recorder := NewAutobiographicalStore(&autobiographicalTestStore{}, nil)
	if _, err := recorder.RecordDecision(context.Background(), DecisionRecord{}, "sess_1", "user_1"); err == nil {
		t.Fatal("expected error for empty decision")
	}
}
