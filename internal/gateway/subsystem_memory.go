package gateway

import (
	"context"
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/cortex"
	"github.com/Forest-Isle/IronClaw/internal/knowledge"
	"github.com/Forest-Isle/IronClaw/internal/knowledge/graph"
	"github.com/Forest-Isle/IronClaw/internal/memory"
)

// MemorySubsystem manages memory store, knowledge searcher, knowledge graph,
// fact extraction, lifecycle management, and background tasks for consolidation,
// compaction, and graph decay.
type MemorySubsystem struct {
	memStore      memory.Store
	embedder      memory.EmbeddingProvider
	factExtractor *memory.LLMFactExtractor
	lifecycleMgr  *memory.LifecycleManager
	consolidator  *memory.Consolidator
	compactor     *memory.Compactor
	graphDecay    *graph.GraphDecayTask
	kbSearcher    knowledge.Searcher
	graphStore    graph.Graph
	cortex        *cortex.UnifiedRetriever
	memoryDir     string
}

func (ms *MemorySubsystem) Name() string { return "memory" }

// Start is a no-op — all memory components are initialized during New().
func (ms *MemorySubsystem) Start(_ context.Context) error { return nil }

// Stop shuts down all memory background tasks.
func (ms *MemorySubsystem) Stop(_ context.Context) error {
	if ms.consolidator != nil {
		ms.consolidator.Stop()
		slog.Debug("memory: consolidator stopped")
	}
	if ms.compactor != nil {
		ms.compactor.Stop()
		slog.Debug("memory: compactor stopped")
	}
	if ms.graphDecay != nil {
		ms.graphDecay.Stop()
		slog.Debug("memory: graph decay stopped")
	}
	return nil
}

// Store returns the memory store, or nil if memory is not enabled.
func (ms *MemorySubsystem) Store() memory.Store { return ms.memStore }

// Embedder returns the memory embedding provider, or nil.
func (ms *MemorySubsystem) Embedder() memory.EmbeddingProvider { return ms.embedder }

// FactExtractor returns the fact extractor, or nil.
func (ms *MemorySubsystem) FactExtractor() *memory.LLMFactExtractor { return ms.factExtractor }

// LifecycleManager returns the memory lifecycle manager, or nil.
func (ms *MemorySubsystem) LifecycleManager() *memory.LifecycleManager { return ms.lifecycleMgr }

// KBSearcher returns the knowledge base searcher, or nil.
func (ms *MemorySubsystem) KBSearcher() knowledge.Searcher { return ms.kbSearcher }

// GraphStore returns the knowledge graph store, or nil.
func (ms *MemorySubsystem) GraphStore() graph.Graph { return ms.graphStore }

// Cortex returns the unified retriever (cortex), or nil.
func (ms *MemorySubsystem) Cortex() *cortex.UnifiedRetriever { return ms.cortex }

// MemoryDir returns the file-based memory storage directory.
func (ms *MemorySubsystem) MemoryDir() string { return ms.memoryDir }
