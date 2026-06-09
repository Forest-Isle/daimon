package memory

import (
	"context"
	"time"
)

// MemoryConfig holds configuration for the memory subsystem.
// Only fields actually read at runtime are kept.
type MemoryConfig struct {
	FactExtraction      bool
	SimilarityThreshold float64
	BM25Weight          float64
	VectorWeight        float64
	EmbeddingDimension  int
}

// MemoryScope defines the lifetime/visibility of a memory entry.
type MemoryScope string

const (
	ScopeSession MemoryScope = "session"
	ScopeUser    MemoryScope = "user"
	ScopeGlobal  MemoryScope = "global"
)

// MemoryAction is the result of lifecycle decision for a new fact.
type MemoryAction string

const (
	ActionADD    MemoryAction = "ADD"
	ActionUPDATE MemoryAction = "UPDATE"
	ActionDELETE MemoryAction = "DELETE"
	ActionNOOP   MemoryAction = "NOOP"
)

// Entry is a single memory record stored as a Markdown file with YAML frontmatter.
type Entry struct {
	ID        string
	SessionID string
	UserID    string
	Scope     MemoryScope
	Content   string
	Embedding []float32
	Metadata  map[string]string
	ExpiresAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SearchQuery defines parameters for memory search.
type SearchQuery struct {
	Text              string
	Embedding         []float32
	Limit             int
	SessionID         string
	UserID            string
	Scopes            []MemoryScope
	TypeFilter        string
	ExcludeTypes      []string
	IncludeHistorical bool
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
	ListByScope(ctx context.Context, scope MemoryScope, userID string) ([]Entry, error)
	Update(ctx context.Context, id string, content string, version int) error
	Delete(ctx context.Context, id string) error
}
