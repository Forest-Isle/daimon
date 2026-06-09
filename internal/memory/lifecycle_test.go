package memory

import (
	"context"
	"testing"
)

// mockStore implements the Store interface for testing.
type mockStore struct {
	saved   []Entry
	deleted []string
}

func (m *mockStore) Save(_ context.Context, entry Entry) error {
	m.saved = append(m.saved, entry)
	return nil
}

func (m *mockStore) Search(_ context.Context, _ SearchQuery) ([]SearchResult, error) {
	return nil, nil
}

func (m *mockStore) ListByScope(_ context.Context, _ MemoryScope, _ string) ([]Entry, error) {
	return nil, nil
}

func (m *mockStore) Update(_ context.Context, _ string, _ string, _ int) error {
	return nil
}

func (m *mockStore) Delete(_ context.Context, id string) error {
	m.deleted = append(m.deleted, id)
	return nil
}

func TestLifecycleProcessReturnsResult_ADD(t *testing.T) {
	store := &mockStore{}
	lm := NewLifecycleManager(store, nil, nil, MemoryConfig{})

	fact := ExtractedFact{
		Content:  "user likes Go programming",
		Category: "preference",
		Type:     "semantic",
	}

	result, err := lm.Process(context.Background(), fact, "sess1", "user1", ScopeSession)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Action != ActionADD {
		t.Errorf("expected ADD, got %s", result.Action)
	}
	if result.MemoryID == "" {
		t.Error("expected non-empty memory ID")
	}
	if len(store.saved) != 1 {
		t.Errorf("expected 1 saved entry, got %d", len(store.saved))
	}
}

func TestLifecycleProcessNOOP(t *testing.T) {
	store := &mockStore{}

	// Use a mock completer that returns NOOP
	completer := &lifecycleMockCompleter{response: `{"action": "NOOP", "reason": "already captured"}`}

	// Return a high-score candidate so the LLM is consulted
	searchStore := &mockStoreWithSearch{
		mockStore: *store,
		results: []SearchResult{
			{Entry: Entry{ID: "existing1", Content: "user likes Go"}, Score: 0.95},
		},
	}

	lm := NewLifecycleManager(searchStore, nil, completer, MemoryConfig{SimilarityThreshold: 0.8})

	fact := ExtractedFact{
		Content:  "user likes Go",
		Category: "preference",
	}

	result, err := lm.Process(context.Background(), fact, "sess1", "user1", ScopeSession)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Action != ActionNOOP {
		t.Errorf("expected NOOP, got %s", result.Action)
	}
}

func TestMemoryOperationSummary(t *testing.T) {
	tests := []struct {
		name       string
		summary    MemoryOperationSummary
		hasChanges bool
		contains   string
	}{
		{
			name:       "empty",
			summary:    MemoryOperationSummary{},
			hasChanges: false,
		},
		{
			name:       "added only",
			summary:    MemoryOperationSummary{Added: 2},
			hasChanges: true,
			contains:   "+2 added",
		},
		{
			name:       "mixed",
			summary:    MemoryOperationSummary{Added: 1, Updated: 1, Deleted: 1},
			hasChanges: true,
			contains:   "1 updated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.summary.HasChanges() != tt.hasChanges {
				t.Errorf("HasChanges() = %v, want %v", tt.summary.HasChanges(), tt.hasChanges)
			}
			if tt.hasChanges {
				s := tt.summary.String()
				if !containsStr(s, tt.contains) {
					t.Errorf("String() = %q, want to contain %q", s, tt.contains)
				}
				if !containsStr(s, "Memory:") {
					t.Errorf("String() = %q, want to contain emoji prefix", s)
				}
			}
		})
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && findSubstr(s, substr)
}

func findSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// lifecycleMockCompleter returns a fixed response.
type lifecycleMockCompleter struct {
	response string
}

func (m *lifecycleMockCompleter) Complete(_ context.Context, _, _ string) (string, error) {
	return m.response, nil
}

// mockStoreWithSearch wraps mockStore but returns fixed search results.
type mockStoreWithSearch struct {
	mockStore
	results []SearchResult
}

func (m *mockStoreWithSearch) Search(_ context.Context, _ SearchQuery) ([]SearchResult, error) {
	return m.results, nil
}
