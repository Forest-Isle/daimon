package knowledge

import (
	"context"
	"time"
)

// Source represents an ingested document source.
type Source struct {
	ID         string
	URI        string
	SourceType string
	Title      string
	ChunkCount int
	Metadata   map[string]string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Chunk is a single text segment from an ingested document.
type Chunk struct {
	ID         string
	SourceID   string
	SourceURI  string
	SourceType string
	Content    string
	Embedding  []float32
	ChunkIndex int
	Metadata   map[string]string
	CreatedAt  time.Time
}

// KnowledgeResult is a retrieved chunk with relevance score.
type KnowledgeResult struct {
	Chunk Chunk
	Score float64
}

// KnowledgeQuery describes a retrieval request.
type KnowledgeQuery struct {
	Text       string
	Embedding  []float32
	Limit      int
	SourceType string // optional: filter by source type
}

// KnowledgeBase is the main interface for document knowledge storage and retrieval.
type KnowledgeBase interface {
	// Search retrieves relevant chunks using hybrid BM25 + vector search.
	Search(ctx context.Context, query KnowledgeQuery) ([]KnowledgeResult, error)
	// Ingest ingests content from a URI with the given source type.
	Ingest(ctx context.Context, uri, sourceType string) error
	// Sources returns all ingested sources.
	Sources(ctx context.Context) ([]Source, error)
	// DeleteSource removes a source and all its chunks.
	DeleteSource(ctx context.Context, sourceID string) error
}

// Searcher is a minimal interface for knowledge retrieval.
// Both SQLiteKnowledgeBase and HybridRetriever satisfy this interface.
type Searcher interface {
	Search(ctx context.Context, query KnowledgeQuery) ([]KnowledgeResult, error)
}

// EmbeddingProvider generates vector embeddings. Reuses the same interface shape
// as memory.EmbeddingProvider but is redefined here to avoid import coupling.
type EmbeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	Dimensions() int
}

// Completer is a minimal LLM interface for reranking.
type Completer interface {
	Complete(ctx context.Context, systemPrompt, userMessage string) (string, error)
}
