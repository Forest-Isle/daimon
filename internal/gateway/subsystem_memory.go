package gateway

import (
	"context"
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/memorywire"
)

// MemorySubsystem manages memory store, fact extraction, lifecycle management,
// and background tasks for consolidation and compaction.
type MemorySubsystem struct {
	memStore      memory.Store
	embedder      memory.EmbeddingProvider
	factExtractor *memory.LLMFactExtractor
	lifecycleMgr  *memory.LifecycleManager
	consolidator  *memory.Consolidator
	compactor     *memory.Compactor
	cortex        *memory.UnifiedRetriever
	ampAdapter    *memorywire.Adapter
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

// Cortex returns the unified retriever (cortex), or nil.
func (ms *MemorySubsystem) Cortex() *memory.UnifiedRetriever { return ms.cortex }

// MemoryDir returns the file-based memory storage directory.
func (ms *MemorySubsystem) MemoryDir() string { return ms.memoryDir }

// AMPAdapter returns the Memorywire protocol adapter, or nil if memory is disabled.
func (ms *MemorySubsystem) AMPAdapter() *memorywire.Adapter { return ms.ampAdapter }
