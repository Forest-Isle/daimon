package memory

import (
	"context"
	"time"
)

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

// Entry is a single memory.md record. Backward-compatible with existing `memories` table.
type Entry struct {
	ID        string
	SessionID string
	UserID    string      // NEW: identifies the user across sessions
	Scope     MemoryScope // NEW: session | user | global
	Content   string      // preferably a distilled fact, not raw message
	Embedding []float32
	Metadata  map[string]string
	Version   int        // NEW: incremented on each update
	ExpiresAt *time.Time // NEW: optional TTL
	CreatedAt time.Time
	UpdatedAt time.Time // NEW
}

// SearchQuery defines parameters for memory.md search.
type SearchQuery struct {
	Text      string
	Embedding []float32
	Limit     int
	SessionID string        // optional: scope to session
	UserID    string        // optional: scope to user
	Scopes    []MemoryScope // optional: filter by scope(s)
}

// SearchResult is a memory.md entry with a relevance score.
type SearchResult struct {
	Entry Entry
	Score float64
}

// Store is the memory.md storage interface.
type Store interface {
	Save(ctx context.Context, entry Entry) error
	SaveFact(ctx context.Context, entry Entry) error // saves to memory_facts table
	Search(ctx context.Context, query SearchQuery) ([]SearchResult, error)
	ListByScope(ctx context.Context, scope MemoryScope, userID string) ([]Entry, error)
	UpdateFact(ctx context.Context, id string, content string, version int) error
	DeleteFact(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
}
