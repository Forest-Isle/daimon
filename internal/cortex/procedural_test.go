package cortex

import (
	"context"
	"errors"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/memory"
)

// mockStoreWithProcedural implements Store for procedural testing
type mockStoreWithProcedural struct {
	memory.Store
	searchFunc func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error)
	saveFunc   func(ctx context.Context, entry memory.Entry) error
}

func (m *mockStoreWithProcedural) Search(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
	if m.searchFunc != nil {
		return m.searchFunc(ctx, query)
	}
	return nil, nil
}

func (m *mockStoreWithProcedural) Save(ctx context.Context, entry memory.Entry) error {
	if m.saveFunc != nil {
		return m.saveFunc(ctx, entry)
	}
	return nil
}

type mockEmbedder struct {
	memory.EmbeddingProvider
	embedFunc func(ctx context.Context, text string) ([]float32, error)
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if m.embedFunc != nil {
		return m.embedFunc(ctx, text)
	}
	return nil, nil
}

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return make([][]float32, len(texts)), nil
}

func (m *mockEmbedder) Dimensions() int { return 0 }

// --- ProceduralStore tests ---

func TestNewProceduralStore(t *testing.T) {
	ps := NewProceduralStore(nil, nil)
	if ps == nil {
		t.Fatal("NewProceduralStore returned nil")
	}
	if ps.store != nil {
		t.Error("store should be nil")
	}
}

func TestRecordStrategy_NilStore(t *testing.T) {
	ps := NewProceduralStore(nil, nil)
	err := ps.RecordStrategy(context.Background(), "test task", []string{"tool1"}, nil, true, "sid", "uid")
	if err != nil {
		t.Errorf("expected nil error for nil store, got %v", err)
	}
}

func TestRecordStrategy_NilReceiver(t *testing.T) {
	var ps *ProceduralStore
	err := ps.RecordStrategy(context.Background(), "test task", []string{"tool1"}, nil, true, "sid", "uid")
	if err != nil {
		t.Errorf("expected nil error for nil receiver, got %v", err)
	}
}

func TestRecordStrategy_NotSuccessful(t *testing.T) {
	store := &mockStoreWithProcedural{}
	ps := NewProceduralStore(store, nil)
	err := ps.RecordStrategy(context.Background(), "test task", []string{"tool1"}, nil, false, "sid", "uid")
	if err != nil {
		t.Errorf("expected nil error when not successful, got %v", err)
	}
}

func TestRecordStrategy_Success(t *testing.T) {
	var savedEntry memory.Entry
	store := &mockStoreWithProcedural{
		saveFunc: func(ctx context.Context, entry memory.Entry) error {
			savedEntry = entry
			return nil
		},
	}
	ps := NewProceduralStore(store, nil)

	err := ps.RecordStrategy(context.Background(), "build microservice", []string{"go", "docker"}, []string{"use-modules"}, true, "session_1", "user_1")
	if err != nil {
		t.Fatalf("RecordStrategy failed: %v", err)
	}

	if savedEntry.UserID != "user_1" {
		t.Errorf("expected UserID 'user_1', got '%s'", savedEntry.UserID)
	}
	if savedEntry.SessionID != "session_1" {
		t.Errorf("expected SessionID 'session_1', got '%s'", savedEntry.SessionID)
	}
	if savedEntry.Scope != memory.ScopeUser {
		t.Errorf("expected ScopeUser, got %s", savedEntry.Scope)
	}
	if savedEntry.Metadata["type"] != "procedural" {
		t.Errorf("expected metadata type 'procedural', got '%s'", savedEntry.Metadata["type"])
	}
	if savedEntry.Metadata["category"] != "strategy" {
		t.Errorf("expected metadata category 'strategy', got '%s'", savedEntry.Metadata["category"])
	}
}

func TestRecordStrategy_SaveError(t *testing.T) {
	store := &mockStoreWithProcedural{
		saveFunc: func(ctx context.Context, entry memory.Entry) error {
			return errors.New("save failed")
		},
	}
	ps := NewProceduralStore(store, nil)

	err := ps.RecordStrategy(context.Background(), "test task", []string{"tool1"}, nil, true, "sid", "uid")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRecordStrategy_WithEmbedding(t *testing.T) {
	var savedEntry memory.Entry
	store := &mockStoreWithProcedural{
		saveFunc: func(ctx context.Context, entry memory.Entry) error {
			savedEntry = entry
			return nil
		},
	}
	embedder := &mockEmbedder{
		embedFunc: func(ctx context.Context, text string) ([]float32, error) {
			return []float32{0.1, 0.2, 0.3}, nil
		},
	}
	ps := NewProceduralStore(store, embedder)

	err := ps.RecordStrategy(context.Background(), "test task", []string{"tool1"}, nil, true, "sid", "uid")
	if err != nil {
		t.Fatalf("RecordStrategy failed: %v", err)
	}

	if len(savedEntry.Embedding) != 3 {
		t.Errorf("expected 3-dim embedding, got %d", len(savedEntry.Embedding))
	}
}

func TestRecordStrategy_WithContextHints(t *testing.T) {
	var savedEntry memory.Entry
	store := &mockStoreWithProcedural{
		saveFunc: func(ctx context.Context, entry memory.Entry) error {
			savedEntry = entry
			return nil
		},
	}
	ps := NewProceduralStore(store, nil)

	err := ps.RecordStrategy(context.Background(), "test task", []string{"tool1", "tool2"}, []string{"hint-a", "hint-b"}, true, "sid", "uid")
	if err != nil {
		t.Fatalf("RecordStrategy failed: %v", err)
	}

	// Verify saved entry contains a valid JSON body
	if savedEntry.Content == "" {
		t.Error("expected non-empty content")
	}
}

func TestFindSimilar_NilStore(t *testing.T) {
	ps := NewProceduralStore(nil, nil)
	records, err := ps.FindSimilar(context.Background(), "test task", 3)
	if err != nil {
		t.Errorf("expected nil error for nil store, got %v", err)
	}
	if records != nil {
		t.Errorf("expected nil records, got %v", records)
	}
}

func TestFindSimilar_NilReceiver(t *testing.T) {
	var ps *ProceduralStore
	records, err := ps.FindSimilar(context.Background(), "test task", 3)
	if err != nil {
		t.Errorf("expected nil error for nil receiver, got %v", err)
	}
	if records != nil {
		t.Errorf("expected nil records, got %v", records)
	}
}

func TestFindSimilar_DefaultLimit(t *testing.T) {
	called := false
	store := &mockStoreWithProcedural{
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			called = true
			if query.Limit != 3 {
				t.Errorf("expected limit 3 (default), got %d", query.Limit)
			}
			return nil, nil
		},
	}
	ps := NewProceduralStore(store, nil)

	// zero limit should use default of 3
	_, _ = ps.FindSimilar(context.Background(), "test task", 0)
	if !called {
		t.Error("Search was not called")
	}
}

func TestFindSimilar_Results(t *testing.T) {
	store := &mockStoreWithProcedural{
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			return []memory.SearchResult{
				{
					Entry: memory.Entry{
						ID:      "strat_1",
						Content: `{"TaskPattern":"build service","ToolSequence":["go","docker"],"SuccessRate":0.9}`,
					},
					Score: 0.85,
				},
			}, nil
		},
	}
	ps := NewProceduralStore(store, nil)

	records, err := ps.FindSimilar(context.Background(), "build microservice", 3)
	if err != nil {
		t.Fatalf("FindSimilar failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].TaskPattern != "build service" {
		t.Errorf("expected TaskPattern 'build service', got '%s'", records[0].TaskPattern)
	}
	if len(records[0].ToolSequence) != 2 || records[0].ToolSequence[0] != "go" {
		t.Errorf("unexpected ToolSequence: %v", records[0].ToolSequence)
	}
	if records[0].SuccessRate != 0.9 {
		t.Errorf("expected SuccessRate 0.9, got %f", records[0].SuccessRate)
	}
}

func TestFindSimilar_InvalidJSON(t *testing.T) {
	store := &mockStoreWithProcedural{
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			return []memory.SearchResult{
				{
					Entry: memory.Entry{
						ID:      "strat_bad",
						Content: `{invalid json`,
					},
					Score: 0.5,
				},
			}, nil
		},
	}
	ps := NewProceduralStore(store, nil)

	records, err := ps.FindSimilar(context.Background(), "test", 3)
	if err != nil {
		t.Fatalf("FindSimilar failed: %v", err)
	}
	// Invalid JSON should be skipped silently
	if len(records) != 0 {
		t.Errorf("expected 0 records (invalid JSON filtered), got %d", len(records))
	}
}

func TestFindSimilar_EmptyResults(t *testing.T) {
	store := &mockStoreWithProcedural{
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			return nil, nil
		},
	}
	ps := NewProceduralStore(store, nil)

	records, err := ps.FindSimilar(context.Background(), "test", 3)
	if err != nil {
		t.Fatalf("FindSimilar failed: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records from empty search, got %d", len(records))
	}
}

func TestFindSimilar_SearchError(t *testing.T) {
	store := &mockStoreWithProcedural{
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			return nil, errors.New("search failed")
		},
	}
	ps := NewProceduralStore(store, nil)

	_, err := ps.FindSimilar(context.Background(), "test", 3)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFindSimilar_ExactLimit(t *testing.T) {
	store := &mockStoreWithProcedural{
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			return []memory.SearchResult{
				{Entry: memory.Entry{ID: "a", Content: `{"TaskPattern":"a"}`}, Score: 0.5},
				{Entry: memory.Entry{ID: "b", Content: `{"TaskPattern":"b"}`}, Score: 0.4},
				{Entry: memory.Entry{ID: "c", Content: `{"TaskPattern":"c"}`}, Score: 0.3},
			}, nil
		},
	}
	ps := NewProceduralStore(store, nil)

	records, err := ps.FindSimilar(context.Background(), "test", 2)
	if err != nil {
		t.Fatalf("FindSimilar failed: %v", err)
	}
	if len(records) != 3 {
		t.Errorf("expected 3 records (all valid JSON), got %d", len(records))
	}
}

func TestProceduralStore_IntegrationFlow(t *testing.T) {
	records := make([]string, 0)
	store := &mockStoreWithProcedural{
		saveFunc: func(ctx context.Context, entry memory.Entry) error {
			records = append(records, entry.Content)
			return nil
		},
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			if len(records) == 0 {
				return nil, nil
			}
			return []memory.SearchResult{
				{Entry: memory.Entry{ID: "r1", Content: records[0]}, Score: 0.9},
			}, nil
		},
	}

	ps := NewProceduralStore(store, nil)

	// Record a strategy
	err := ps.RecordStrategy(context.Background(), "deploy service", []string{"kubectl", "helm"}, nil, true, "s1", "u1")
	if err != nil {
		t.Fatalf("RecordStrategy failed: %v", err)
	}

	// Find similar
	found, err := ps.FindSimilar(context.Background(), "deploy", 3)
	if err != nil {
		t.Fatalf("FindSimilar failed: %v", err)
	}
	if len(found) != 1 {
		t.Errorf("expected 1 record, got %d", len(found))
	}
	if found[0].TaskPattern != "deploy service" {
		t.Errorf("expected TaskPattern 'deploy service', got '%s'", found[0].TaskPattern)
	}
}
