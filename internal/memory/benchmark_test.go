package memory

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"
)

// mockEmbedder generates random embeddings for testing
type mockEmbedder struct {
	dimension int
	delay     time.Duration
	calls     int
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	m.calls++
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	emb := make([]float32, m.dimension)
	for i := range emb {
		emb[i] = rand.Float32()
	}
	return emb, nil
}

func (m *mockEmbedder) Dimensions() int {
	return m.dimension
}

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		emb, err := m.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		results[i] = emb
	}
	return results, nil
}

// BenchmarkVectorSearch benchmarks file-based memory search
func BenchmarkVectorSearch(b *testing.B) {
	sizes := []int{100, 500}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("FileStore_%d", size), func(b *testing.B) {
			benchmarkFileStoreSearch(b, size)
		})
	}
}

func benchmarkFileStoreSearch(b *testing.B, dataSize int) {
	// Setup temp directory
	memDir := b.TempDir()
	dbPath := fmt.Sprintf("%s/test.db", memDir)

	// Create a simple in-memory DB for index
	f, _ := os.Create(dbPath)
	_ = f.Close()

	embedder := &mockEmbedder{dimension: 128, delay: 0}
	cfg := MemoryConfig{EmbeddingDimension: 128}

	fileStore, err := NewFileMemoryStore(memDir, nil, embedder, cfg)
	if err != nil {
		b.Skip("cannot create file store without DB, skipping benchmark")
		return
	}

	ctx := context.Background()
	for i := 0; i < dataSize; i++ {
		entry := Entry{
			ID:        fmt.Sprintf("fact_%d", i),
			SessionID: "test_session",
			UserID:    "test_user",
			Scope:     ScopeUser,
			Content:   fmt.Sprintf("Test fact number %d with some content", i),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		emb, _ := embedder.Embed(ctx, entry.Content)
		entry.Embedding = emb

		if err := fileStore.Save(ctx, entry); err != nil {
			b.Fatal(err)
		}
	}

	queryEmb, _ := embedder.Embed(ctx, "test query")

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		query := SearchQuery{
			Text:      "test query",
			Embedding: queryEmb,
			Limit:     10,
			UserID:    "test_user",
		}

		_, err := fileStore.Search(ctx, query)
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
		cachedEmbedder := NewCachedEmbedder(baseEmbedder)
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
	cachedEmbedder := NewCachedEmbedder(baseEmbedder)

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

	if baseEmbedder.calls != uniqueQueries {
		t.Errorf("base embedder calls = %d, want %d unique queries", baseEmbedder.calls, uniqueQueries)
	}

	t.Logf("Cache hit rate: %.1f%% (expected: %.1f%%)", expectedHitRate*100, expectedHitRate*100)
	t.Logf("Total time: %v (avg per query: %v)", elapsed, elapsed/time.Duration(totalCalls))
}
