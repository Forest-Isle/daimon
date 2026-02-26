package memory

import (
	"context"
	"time"
)

// Entry is a single memory record.
type Entry struct {
	ID        string
	SessionID string
	Content   string
	Embedding []float32
	Metadata  map[string]string
	CreatedAt time.Time
}

// SearchQuery defines parameters for memory search.
type SearchQuery struct {
	Text      string
	Embedding []float32
	Limit     int
	SessionID string // optional: scope to session
}

// SearchResult is a memory entry with a relevance score.
type SearchResult struct {
	Entry Entry
	Score float64
}

// Store is the memory storage interface.
type Store interface {
	Save(ctx context.Context, entry Entry) error
	Search(ctx context.Context, query SearchQuery) ([]SearchResult, error)
	Delete(ctx context.Context, id string) error
}
