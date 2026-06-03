package memory

import "time"

// MemoryType categorizes a memory unit in the unified cortex.
type MemoryType string

const (
	Episodic   MemoryType = "episodic"
	Semantic   MemoryType = "semantic"
	Procedural MemoryType = "procedural"
	Profile    MemoryType = "profile"
)

// UnifiedMemory is returned by UnifiedRetriever.Search.
type UnifiedMemory struct {
	ID      string
	Type    MemoryType
	Content string
	Score   float64
	Source  string // "memory", "knowledge", "graph", "procedural"

	// Procedural-specific
	Strategy *StrategyRecord `json:",omitempty"`
}

// StrategyRecord captures a successful task execution pattern for procedural memory.
type StrategyRecord struct {
	TaskPattern  string
	ToolSequence []string
	ContextHints []string
	SuccessRate  float64
	LastUsed     time.Time
}

// TripleSummary is a compact representation of a graph triple.
type TripleSummary struct {
	Subject   string
	Predicate string
	Object    string
}

// SearchOptions configures a cortex search.
type SearchOptions struct {
	UserID    string
	SessionID string
	Limit     int // per-source limit; 0 = default (5)
}

// FusionWeights controls how scores from different sources are combined.
type FusionWeights struct {
	MemoryWeight     float64
	KnowledgeWeight  float64
	GraphWeight      float64
	ProceduralWeight float64
}

// DefaultFusionWeights returns sensible defaults.
func DefaultFusionWeights() *FusionWeights {
	return &FusionWeights{
		MemoryWeight:     0.35,
		KnowledgeWeight:  0.30,
		GraphWeight:      0.15,
		ProceduralWeight: 0.20,
	}
}
