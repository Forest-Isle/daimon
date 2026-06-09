package gateway

import (
	"context"
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/memory"
)

// MemorySubsystem manages memory store, fact extraction, and lifecycle management.
type MemorySubsystem struct {
	memStore      memory.Store
	embedder      memory.EmbeddingProvider
	factExtractor *memory.LLMFactExtractor
	lifecycleMgr  *memory.LifecycleManager
	cortex        *memory.UnifiedRetriever
	memoryDir     string
}

func (ms *MemorySubsystem) Name() string { return "memory" }

func (ms *MemorySubsystem) Start(_ context.Context) error { return nil }

func (ms *MemorySubsystem) Stop(_ context.Context) error {
	slog.Debug("memory: subsystem stopped")
	return nil
}

func (ms *MemorySubsystem) Store() memory.Store                     { return ms.memStore }
func (ms *MemorySubsystem) Embedder() memory.EmbeddingProvider       { return ms.embedder }
func (ms *MemorySubsystem) FactExtractor() *memory.LLMFactExtractor  { return ms.factExtractor }
func (ms *MemorySubsystem) LifecycleManager() *memory.LifecycleManager { return ms.lifecycleMgr }
func (ms *MemorySubsystem) Cortex() *memory.UnifiedRetriever         { return ms.cortex }
func (ms *MemorySubsystem) MemoryDir() string                        { return ms.memoryDir }
