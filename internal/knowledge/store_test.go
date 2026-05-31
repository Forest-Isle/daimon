//go:build fts5

package knowledge

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

func newTestStore(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	return db
}

func noopEmbedder() *testEmbedder {
	return &testEmbedder{}
}

type testEmbedder struct{}

func (e *testEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	// Return a deterministic embedding based on text length
	dim := 4
	emb := make([]float32, dim)
	for i := range emb {
		emb[i] = float32(len(text)) / 100.0
	}
	return emb, nil
}

func (e *testEmbedder) Dimensions() int { return 4 }

func TestNewSQLiteKnowledgeBase(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{})
	if kb == nil {
		t.Fatal("expected non-nil knowledge base")
	}
	if kb.GetPipeline() == nil {
		t.Error("expected non-nil pipeline")
	}
}

func TestSQLiteKnowledgeBase_SaveSource(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{})
	ctx := context.Background()

	id, err := kb.saveSource(ctx, "test-uri", "markdown", "Test Title")
	if err != nil {
		t.Fatalf("saveSource: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty source ID")
	}

	// Save same URI again — should return same ID
	id2, err := kb.saveSource(ctx, "test-uri", "markdown", "Updated Title")
	if err != nil {
		t.Fatalf("saveSource duplicate: %v", err)
	}
	if id2 != id {
		t.Errorf("expected same ID for duplicate URI, got %s vs %s", id2, id)
	}
}

func TestSQLiteKnowledgeBase_SaveAndSearchChunk(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{})
	ctx := context.Background()

	sourceID, err := kb.saveSource(ctx, "uri:test", "markdown", "Test Doc")
	if err != nil {
		t.Fatalf("saveSource: %v", err)
	}

	chunk := Chunk{
		ID:         "chunk_test_1",
		SourceID:   sourceID,
		SourceURI:  "uri:test",
		SourceType: "markdown",
		Content:    "The quick brown fox jumps over the lazy dog",
		ChunkIndex: 0,
	}
	if err := kb.saveChunk(ctx, chunk); err != nil {
		t.Fatalf("saveChunk: %v", err)
	}

	// Search via likeSearch
	results, err := kb.likeSearch(ctx, KnowledgeQuery{Text: "fox", Limit: 10})
	if err != nil {
		t.Fatalf("likeSearch: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result from LIKE search")
	}
	if results[0].Chunk.ID != "chunk_test_1" {
		t.Errorf("expected chunk_test_1, got %s", results[0].Chunk.ID)
	}

	// Search with no match
	results, err = kb.likeSearch(ctx, KnowledgeQuery{Text: "zzzznotfound", Limit: 10})
	if err != nil {
		t.Fatalf("likeSearch no match: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for non-matching query, got %d", len(results))
	}
}

func TestSQLiteKnowledgeBase_Sources(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{})
	ctx := context.Background()

	// No sources yet
	sources, err := kb.Sources(ctx)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(sources) != 0 {
		t.Errorf("expected 0 sources initially, got %d", len(sources))
	}

	kb.saveSource(ctx, "uri:a", "markdown", "Doc A")
	kb.saveSource(ctx, "uri:b", "text", "Doc B")

	sources, err = kb.Sources(ctx)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(sources))
	}
}

func TestSQLiteKnowledgeBase_DeleteSource(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{})
	ctx := context.Background()

	id, _ := kb.saveSource(ctx, "uri:delete-me", "markdown", "To Delete")

	if err := kb.DeleteSource(ctx, id); err != nil {
		t.Fatalf("DeleteSource: %v", err)
	}

	sources, _ := kb.Sources(ctx)
	for _, s := range sources {
		if s.ID == id {
			t.Errorf("source %s still exists after delete", id)
		}
	}
}

func TestSQLiteKnowledgeBase_ChunkBatch(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{})
	ctx := context.Background()

	sourceID, _ := kb.saveSource(ctx, "uri:batch", "text", "Batch Test")
	for i := 0; i < 5; i++ {
		chunk := Chunk{
			ID:         "batch_" + itoa(i),
			SourceID:   sourceID,
			SourceURI:  "uri:batch",
			SourceType: "text",
			Content:    "content number " + itoa(i),
			ChunkIndex: i,
		}
		if err := kb.saveChunk(ctx, chunk); err != nil {
			t.Fatalf("saveChunk batch %d: %v", i, err)
		}
	}

	results, err := kb.likeSearch(ctx, KnowledgeQuery{Text: "content", Limit: 20})
	if err != nil {
		t.Fatalf("likeSearch: %v", err)
	}
	if len(results) != 5 {
		t.Errorf("expected 5 results, got %d", len(results))
	}
}

func TestSQLiteKnowledgeBase_SearchLimits(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{})
	ctx := context.Background()

	sourceID, _ := kb.saveSource(ctx, "uri:limits", "text", "Limit Test")
	for i := 0; i < 20; i++ {
		chunk := Chunk{
			ID:         "limit_" + itoa(i),
			SourceID:   sourceID,
			SourceURI:  "uri:limits",
			SourceType: "text",
			Content:    "searchable content item " + itoa(i),
			ChunkIndex: i,
		}
		kb.saveChunk(ctx, chunk)
	}

	results, err := kb.likeSearch(ctx, KnowledgeQuery{Text: "searchable", Limit: 5})
	if err != nil {
		t.Fatalf("likeSearch: %v", err)
	}
	// likeSearch multiplies limit by 3 internally, so max is 15
	if len(results) > 15 {
		t.Errorf("expected at most 15 results (limit 5 * 3), got %d", len(results))
	}
	if len(results) == 0 {
		t.Error("expected at least 1 result")
	}
}

func TestSQLiteKnowledgeBase_ConfigDefaults(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{
		EnableSearchCache: true,
	})
	if kb.searchCache == nil {
		t.Error("expected search cache to be initialized")
	}
	if kb.searchCache.maxSize != 500 {
		t.Errorf("expected default cache size 500, got %d", kb.searchCache.maxSize)
	}
}

func TestSQLiteKnowledgeBase_InvalidateCache(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{
		EnableSearchCache: true,
	})

	kb.InvalidateCache()
	// Just verify it doesn't panic when cache is enabled
	kb.InvalidateCache() // call again for coverage
}

func TestSQLiteKnowledgeBase_OptimizeFTS(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{})
	ctx := context.Background()
	// Should not panic or error
	kb.optimizeFTS(ctx)
}

func TestHybridRetriever_NoopReranker(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{})
	ctx := context.Background()

	sourceID, _ := kb.saveSource(ctx, "uri:retriever", "text", "Retriever Test")
	kb.saveChunk(ctx, Chunk{
		ID: "retriever_chunk", SourceID: sourceID, SourceURI: "uri:retriever",
		SourceType: "text", Content: "retrievable content", ChunkIndex: 0,
	})

	retriever := NewHybridRetriever(kb, nil) // nil -> NoopReranker
	results, err := retriever.Search(ctx, KnowledgeQuery{Text: "retrievable", Limit: 10})
	if err != nil {
		t.Fatalf("HybridRetriever.Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result from hybrid retriever")
	}
}

func TestSQLiteKnowledgeBase_UpdateChunkCount(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{})
	ctx := context.Background()

	sourceID, _ := kb.saveSource(ctx, "uri:count", "text", "Count Test")
	for i := 0; i < 3; i++ {
		kb.saveChunk(ctx, Chunk{
			ID: "count_" + itoa(i), SourceID: sourceID, SourceURI: "uri:count",
			SourceType: "text", Content: "chunk " + itoa(i), ChunkIndex: i,
		})
	}

	kb.updateChunkCount(ctx, sourceID)

	sources, _ := kb.Sources(ctx)
	for _, s := range sources {
		if s.ID == sourceID && s.ChunkCount != 3 {
			t.Errorf("expected chunk_count 3, got %d", s.ChunkCount)
		}
	}
}

func TestNewWithSearchCache(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{
		EnableSearchCache: true,
		SearchCacheSize:   50,
		SearchCacheTTL:    defaultTestTTL,
	})
	if kb.searchCache == nil {
		t.Fatal("expected search cache to be initialized")
	}
	if kb.searchCache.maxSize != 50 {
		t.Errorf("expected cache size 50, got %d", kb.searchCache.maxSize)
	}
}

// itoa is a simple int-to-string helper (avoid strconv import).
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

var defaultTestTTL = time.Hour
