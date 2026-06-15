package gateway

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/memory"
	"github.com/Forest-Isle/daimon/internal/mind"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/tool"
)

type MemorySubsystem struct {
	memStore     memory.Store
	embedder     memory.EmbeddingProvider
	lifecycleMgr *memory.LifecycleManager
	cortex       *memory.UnifiedRetriever
	memoryDir    string
}

func (ms *MemorySubsystem) Name() string                  { return "memory" }
func (ms *MemorySubsystem) Start(_ context.Context) error { return nil }
func (ms *MemorySubsystem) Stop(_ context.Context) error  { return nil }

func (ms *MemorySubsystem) Store() memory.Store                        { return ms.memStore }
func (ms *MemorySubsystem) Embedder() memory.EmbeddingProvider         { return ms.embedder }
func (ms *MemorySubsystem) LifecycleManager() *memory.LifecycleManager { return ms.lifecycleMgr }
func (ms *MemorySubsystem) Cortex() *memory.UnifiedRetriever           { return ms.cortex }
func (ms *MemorySubsystem) MemoryDir() string                          { return ms.memoryDir }

func InitMemorySystem(features *FeatureSubsystem, cfg *config.Config, builder *agent.DepsBuilder, provider mind.Provider, db *store.DB, toolsReg *tool.Registry) *MemorySubsystem {
	if !features.IsEnabled("memory") {
		return &MemorySubsystem{}
	}
	ms := &MemorySubsystem{}

	var embedder memory.EmbeddingProvider = &memory.NoopEmbedding{}
	if cfg.Memory.OpenAIAPIKey != "" {
		embedder = memory.NewCachedEmbedder(memory.NewOpenAIEmbeddingWithURL(cfg.Memory.OpenAIAPIKey, cfg.Memory.EmbeddingModel, cfg.Memory.EmbeddingBaseURL))
		slog.Info("memory: cached embedder enabled")
	}
	ms.embedder = embedder

	memCfg := memory.MemoryConfig{
		FactExtraction: cfg.Memory.FactExtraction, SimilarityThreshold: cfg.Memory.SimilarityThreshold,
		BM25Weight: cfg.Memory.BM25Weight, VectorWeight: cfg.Memory.VectorWeight,
		EmbeddingDimension: cfg.Memory.VectorDimension,
	}

	storageDir := cfg.Memory.StorageDir
	if storageDir == "" {
		storageDir = filepath.Join(appdir.BaseDir(), "memory")
	} else if strings.HasPrefix(storageDir, "~/") {
		storageDir = filepath.Join(filepath.Dir(appdir.BaseDir()), storageDir[2:])
	}

	fileStore, err := memory.NewFileMemoryStore(storageDir, db.DB, embedder, memCfg)
	if err != nil {
		slog.Warn("memory: create file store failed", "err", err)
		return ms
	}
	ms.memStore = fileStore
	ms.memoryDir = storageDir
	slog.Info("memory: file-based storage enabled", "dir", storageDir)

	if cfg.Memory.EnableSearchCache {
		sz, ttl := cfg.Memory.SearchCacheSize, cfg.Memory.SearchCacheTTL
		if sz <= 0 {
			sz = 500
		}
		if ttl <= 0 {
			ttl = 5 * time.Minute
		}
		ms.memStore = memory.NewCachedStore(ms.memStore, sz, ttl)
		slog.Info("memory: search cache enabled", "size", sz, "ttl", ttl)
	}

	builder.Memory.Store = ms.memStore
	builder.Memory.BaseDir = storageDir

	if cfg.Memory.FactExtraction {
		completer := &completerAdapter{provider: provider, model: cfg.LLM.Model}
		ms.lifecycleMgr = memory.NewLifecycleManager(ms.memStore, embedder, completer, memCfg)
		ms.lifecycleMgr.SetAuditLogger(memory.NewAuditLogger(db.DB))
		builder.Memory.LifecycleMgr = ms.lifecycleMgr
		slog.Info("memory: lifecycle management enabled")
	}

	if toolsReg != nil {
		toolsReg.Register(tool.NewMemoryTool(ms.memStore, ms.lifecycleMgr))
		slog.Info("memory: unified memory tool registered")
	}

	return ms
}

func (ms *MemorySubsystem) BuildCortex() {
	if ms.memStore != nil && ms.embedder != nil {
		procedural := memory.NewProceduralStore(ms.memStore, ms.embedder)
		ms.cortex = memory.NewUnifiedRetriever(ms.memStore, procedural, ms.embedder)
	}
}
