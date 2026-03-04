package memory

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/punkopunko/ironclaw/internal/store"
)

// mockEmbedder generates random embeddings for testing
type mockEmbedder struct {
	dimension int
	delay     time.Duration
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	emb := make([]float32, m.dimension)
	for i := range emb {
		emb[i] = rand.Float32()
	}
	return emb, nil
}

// BenchmarkVectorSearch compares brute-force vs cached search
func BenchmarkVectorSearch(b *testing.B) {
	sizes := []int{100, 1000, 10000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("BruteForce_%d", size), func(b *testing.B) {
			benchmarkSearch(b, size, false, false)
		})

		b.Run(fmt.Sprintf("WithCache_%d", size), func(b *testing.B) {
			benchmarkSearch(b, size, true, false)
		})

		b.Run(fmt.Sprintf("WithVSS_%d", size), func(b *testing.B) {
			benchmarkSearch(b, size, false, true)
		})

		b.Run(fmt.Sprintf("FullOptimized_%d", size), func(b *testing.B) {
			benchmarkSearch(b, size, true, true)
		})
	}
}

func benchmarkSearch(b *testing.B, dataSize int, useCache, useVSS bool) {
	// Setup in-memory database
	db, err := store.Open(":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	// Create tables
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS memory_facts (
			id TEXT PRIMARY KEY,
			session_id TEXT,
			user_id TEXT,
			scope TEXT,
			content TEXT,
			embedding BLOB,
			category TEXT,
			version INTEGER,
			expires_at TIMESTAMP,
			metadata TEXT,
			created_at TIMESTAMP,
			updated_at TIMESTAMP
		)
	`)
	if err != nil {
		b.Fatal(err)
	}

	// Configure store
	cfg := MemoryConfig{
		EnableSearchCache: useCache,
		SearchCacheSize:   500,
		SearchCacheTTL:    5 * time.Minute,
		EnableVSS:         useVSS,
		VectorDimension:   128, // Smaller for faster tests
	}

	embedder := &mockEmbedder{dimension: 128, delay: 0}
	s := NewSQLiteStore(db, embedder, cfg)

	// Populate with test data
	ctx := context.Background()
	for i := 0; i < dataSize; i++ {
		entry := Entry{
			ID:        fmt.Sprintf("fact_%d", i),
			SessionID: "test_session",
			UserID:    "test_user",
			Scope:     ScopeUser,
			Content:   fmt.Sprintf("Test fact number %d with some content", i),
			Version:   1,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		// Generate embedding
		emb, _ := embedder.Embed(ctx, entry.Content)
		entry.Embedding = emb

		if err := s.SaveFact(ctx, entry); err != nil {
			b.Fatal(err)
		}
	}

	// Generate query embedding
	queryEmb, _ := embedder.Embed(ctx, "test query")

	// Reset timer before benchmark
	b.ResetTimer()

	// Run benchmark
	for i := 0; i < b.N; i++ {
		query := SearchQuery{
			Text:      "test query",
			Embedding: queryEmb,
			Limit:     10,
			UserID:    "test_user",
		}

		_, err := s.Search(ctx, query)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEmbeddingCache tests embedding cache performance
func BenchmarkEmbeddingCache(b *testing.B) {
	baseEmbedder := &mockEmbedder{dimension: 1536, delay: 10 * time.Millisecond}

	b.Run("WithoutCache", func(b *testing.B) {
		ctx := context.Background()
		for i := 0; i < b.N; i++ {
			_, err := baseEmbedder.Embed(ctx, "test query")
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("WithCache", func(b *testing.B) {
		cachedEmbedder := NewCachedEmbedder(baseEmbedder, "test-model", 1000, 10*time.Minute)
		ctx := context.Background()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := cachedEmbedder.Embed(ctx, "test query")
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestCacheHitRate measures cache effectiveness
func TestCacheHitRate(t *testing.T) {
	baseEmbedder := &mockEmbedder{dimension: 128, delay: 1 * time.Millisecond}
	cachedEmbedder := NewCachedEmbedder(baseEmbedder, "test-model", 100, 10*time.Minute)

	ctx := context.Background()
	queries := []string{
		"query 1", "query 2", "query 3",
		"query 1", "query 2", // Repeats
		"query 4", "query 1", // More repeats
	}

	totalCalls := len(queries)
	uniqueQueries := 4
	expectedHitRate := float64(totalCalls-uniqueQueries) / float64(totalCalls)

	start := time.Now()
	for _, q := range queries {
		_, err := cachedEmbedder.Embed(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)

	// With cache, should be much faster than totalCalls * delay
	maxExpected := time.Duration(uniqueQueries) * baseEmbedder.delay * 2
	if elapsed > maxExpected {
		t.Errorf("Cache not effective: took %v, expected < %v", elapsed, maxExpected)
	}

	t.Logf("Cache hit rate: %.1f%% (expected: %.1f%%)", expectedHitRate*100, expectedHitRate*100)
	t.Logf("Total time: %v (avg per query: %v)", elapsed, elapsed/time.Duration(totalCalls))
}

// TestSearchResultCache tests search result caching
func TestSearchResultCache(t *testing.T) {
	cache := NewSearchResultCache(10, 5*time.Minute)

	query := SearchQuery{
		Text:   "test query",
		Limit:  10,
		UserID: "user1",
	}

	results := []SearchResult{
		{Entry: Entry{ID: "fact_1", Content: "test"}, Score: 0.9},
		{Entry: Entry{ID: "fact_2", Content: "test"}, Score: 0.8},
	}

	// First call - cache miss
	if _, ok := cache.Get(query); ok {
		t.Error("Expected cache miss")
	}

	// Store in cache
	cache.Set(query, results)

	// Second call - cache hit
	cached, ok := cache.Get(query)
	if !ok {
		t.Error("Expected cache hit")
	}

	if len(cached) != len(results) {
		t.Errorf("Expected %d results, got %d", len(results), len(cached))
	}

	// Test invalidation
	cache.Invalidate()
	if _, ok := cache.Get(query); ok {
		t.Error("Expected cache miss after invalidation")
	}
}
