package memory

import (
	"context"
	"time"
)

// MemoryConfig holds configuration for the memory subsystem.
type MemoryConfig struct {
	FactExtraction        bool
	SimilarityThreshold   float64
	ConsolidationInterval time.Duration
	BM25Weight            float64
	VectorWeight          float64
	EnableVSS             bool          // Enable HNSW indexing via sqlite-vss
	VectorDimension       int           // Embedding dimension (default: 1536 for OpenAI)
	EnableSearchCache     bool          // Enable search result caching
	SearchCacheSize       int           // Max cached queries (default: 500)
	SearchCacheTTL        time.Duration // Cache TTL (default: 5min)
}

// MemoryScope defines the lifetime/visibility of a memory.md entry.
type MemoryScope string

const (
	ScopeSession MemoryScope = "session" // short-lived, scoped to a conversation
	ScopeUser    MemoryScope = "user"    // long-lived, across conversations
	ScopeGlobal  MemoryScope = "global"  // system-level, shared
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
	UserID    string      // identifies the user across sessions
	Scope     MemoryScope // session | user | global
	Content   string      // preferably a distilled fact, not raw message
	Embedding []float32
	Metadata  map[string]string
	Version   int        // incremented on each update
	ExpiresAt *time.Time // optional TTL
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SearchQuery defines parameters for memory search.
type SearchQuery struct {
	Text      string
	Embedding []float32
	Limit     int
	SessionID string        // optional: scope to session
	UserID    string        // optional: scope to user
	Scopes    []MemoryScope // optional: filter by scope(s)
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
