package memory

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
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

func TestLifecycleUpdateSoftInvalidatesAndMarksConflict(t *testing.T) {
	store := &mockTemporalStore{
		results: []SearchResult{
			{Entry: Entry{ID: "existing1", Content: "Carol lives in San Francisco."}, Score: 0.95},
		},
	}
	completer := &lifecycleMockCompleter{response: `{"action":"UPDATE","target_id":"existing1","reason":"location changed","conflicting_ids":["existing1"]}`}
	lm := NewLifecycleManager(store, nil, completer, MemoryConfig{SimilarityThreshold: 0.8})

	result, err := lm.Process(context.Background(), ExtractedFact{
		Content:  "Carol lives in New York.",
		Category: "fact",
		Type:     "semantic",
	}, "sess1", "user1", ScopeUser)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if result.Action != ActionUPDATE {
		t.Fatalf("action = %s, want UPDATE", result.Action)
	}
	if len(store.invalidated) != 1 || store.invalidated[0] != "existing1" {
		t.Fatalf("invalidated = %#v, want [existing1]", store.invalidated)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("expected no hard deletes, got %#v", store.deleted)
	}
	if len(store.saved) != 1 {
		t.Fatalf("saved entries = %d, want 1", len(store.saved))
	}
	if got := store.saved[0].Metadata["updated_from"]; got != "existing1" {
		t.Fatalf("updated_from = %q, want existing1", got)
	}
	if got := store.saved[0].Metadata["conflicting_ids"]; got != "existing1" {
		t.Fatalf("conflicting_ids = %q, want existing1", got)
	}
}

func TestLifecycleAuditLoggerRecordsDecision(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS memory_audit_log (
			id TEXT PRIMARY KEY,
			memory_id TEXT NOT NULL,
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			timestamp DATETIME NOT NULL,
			details TEXT
		)
	`); err != nil {
		t.Fatal(err)
	}

	store := &mockStore{}
	lm := NewLifecycleManager(store, nil, nil, MemoryConfig{})
	lm.SetAuditLogger(NewAuditLogger(db))

	if _, err := lm.Process(context.Background(), ExtractedFact{
		Content:  "user prefers concise summaries",
		Category: "preference",
		Type:     "semantic",
	}, "sess1", "user1", ScopeUser); err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	var action, actor string
	if err := db.QueryRow("SELECT action, actor FROM memory_audit_log LIMIT 1").Scan(&action, &actor); err != nil {
		t.Fatal(err)
	}
	if action != string(ActionADD) || actor != "lifecycle" {
		t.Fatalf("audit row action/actor = %s/%s", action, actor)
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

type mockTemporalStore struct {
	mockStore
	results     []SearchResult
	invalidated []string
}

func (m *mockTemporalStore) Search(_ context.Context, _ SearchQuery) ([]SearchResult, error) {
	return m.results, nil
}

func (m *mockTemporalStore) SoftInvalidate(_ context.Context, id string) error {
	m.invalidated = append(m.invalidated, id)
	return nil
}
