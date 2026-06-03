package gateway

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

func (gw *Gateway) initMemorySystem() error {
	if !gw.featureEnabled("memory") {
		return nil
	}

	cfg := gw.Config()
	var embedder memory.EmbeddingProvider = &memory.NoopEmbedding{}
	if cfg.Memory.OpenAIAPIKey != "" {
		baseEmbedder := memory.NewOpenAIEmbeddingWithURL(cfg.Memory.OpenAIAPIKey, cfg.Memory.EmbeddingModel, cfg.Memory.EmbeddingBaseURL)
		embedder = memory.NewCachedEmbedder(baseEmbedder)
		slog.Info("memory: cached embedder enabled")
	}
	gw.memory.embedder = embedder
	memCfg := memory.MemoryConfig{
		FactExtraction:           cfg.Memory.FactExtraction,
		SimilarityThreshold:      cfg.Memory.SimilarityThreshold,
		ConsolidationInterval:    cfg.Memory.ConsolidationInterval,
		BM25Weight:               cfg.Memory.BM25Weight,
		VectorWeight:             cfg.Memory.VectorWeight,
		EnableVSS:                cfg.Memory.EnableVSS,
		VectorDimension:          cfg.Memory.VectorDimension,
		EnableSearchCache:        cfg.Memory.EnableSearchCache,
		SearchCacheSize:          cfg.Memory.SearchCacheSize,
		SearchCacheTTL:           cfg.Memory.SearchCacheTTL,
		ReflectionCountThreshold: cfg.Memory.ReflectionCountThreshold,
		ReflectionDriftThreshold: cfg.Memory.ReflectionDriftThreshold,
		ReflectionL2Trigger:      cfg.Memory.ReflectionL2Trigger,
	}

	// File-based storage
	storageDir := cfg.Memory.StorageDir
	if storageDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		storageDir = filepath.Join(home, ".IronClaw", "memory")
	} else if strings.HasPrefix(storageDir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve storage_dir tilde: %w", err)
		}
		storageDir = filepath.Join(home, storageDir[2:])
	}

	// Create file store
	fileStore, err := memory.NewFileMemoryStore(storageDir, gw.db.DB, embedder, memCfg)
	if err != nil {
		return fmt.Errorf("create file memory store: %w", err)
	}
	gw.memory.memStore = fileStore
	gw.memory.memoryDir = storageDir

	slog.Info("memory: file-based storage enabled", "dir", storageDir)

	// Wrap with search cache if enabled
	if cfg.Memory.EnableSearchCache {
		cacheSize := cfg.Memory.SearchCacheSize
		if cacheSize <= 0 {
			cacheSize = 500
		}
		cacheTTL := cfg.Memory.SearchCacheTTL
		if cacheTTL <= 0 {
			cacheTTL = 5 * time.Minute
		}
		gw.memory.memStore = memory.NewCachedStore(gw.memory.memStore, cacheSize, cacheTTL)
		slog.Info("memory: search cache enabled", "size", cacheSize, "ttl", cacheTTL)
	}

	// Update agentDeps with the memory store
	gw.agentDeps.Memory.Store = gw.memory.memStore
	gw.agentDeps.Memory.BaseDir = storageDir

	// Initialize forgetting curve manager
	forgettingCurve := memory.NewForgettingCurveManager(gw.db)

	if cfg.Memory.FactExtraction {
		completer := &completerAdapter{provider: gw.provider, model: cfg.LLM.Model}
		gw.memory.factExtractor = memory.NewLLMFactExtractor(completer, memCfg)

		// Create reflection tracker for automatic L1/L2 reflections
		reflector := memory.NewReflectionTracker(gw.memory.memStore, completer, embedder, memCfg, gw.db.DB)
		slog.Info("memory: reflection tracker enabled")

		gw.memory.lifecycleMgr = memory.NewLifecycleManager(gw.memory.memStore, embedder, completer, memCfg, reflector)
		gw.memory.lifecycleMgr.SetAuditLogger(memory.NewAuditLogger(gw.db.DB))

		// Start compactor background task
		compactor := memory.NewCompactor(gw.memory.memStore, completer, gw.db.DB, storageDir, memCfg)
		gw.memory.compactor = compactor
		compactor.Start(gw.initCtx)
		slog.Info("memory: compactor enabled")

		// Create profiler and wire it to the reflection tracker
		profiler := memory.NewProfiler(gw.memory.memStore, completer, gw.db.DB, storageDir, memCfg)
		reflector.SetProfilerCallback(profiler)
		gw.agentDeps.Memory.Profiler = profiler
		slog.Info("memory: profiler created and wired to reflection tracker")

		if err := profiler.MigrateLegacyProfile(gw.initCtx, "default"); err != nil {
			slog.Warn("memory: legacy profile migration failed", "err", err)
		}
	}

	// Wire fact extractor and lifecycle manager to agent deps (if enabled).
	// These may be nil when FactExtraction is disabled; the agent does nil checks.
	if gw.memory.factExtractor != nil {
		gw.agentDeps.Memory.FactExtractor = gw.memory.factExtractor
	}
	if gw.memory.lifecycleMgr != nil {
		gw.agentDeps.Memory.LifecycleMgr = gw.memory.lifecycleMgr
	}

	// Register memory_manage tool
	memTool := tool.NewMemoryManageTool(gw.memory.memStore, gw.db.DB, storageDir)
	gw.tools.Register(memTool)
	slog.Info("memory: memory_manage tool registered")

	// Start consolidator background task (promotes session facts to user scope)
	gw.memory.consolidator = memory.NewConsolidator(gw.memory.memStore, gw.db.DB, storageDir, cfg.Memory.ConsolidationInterval)
	gw.memory.consolidator.Start(gw.initCtx)
	slog.Info("memory: consolidator enabled")

	// Schedule daily retention policy enforcement alongside fade task
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := forgettingCurve.FadeWeakMemoriesFromFiles(gw.initCtx, storageDir); err != nil {
					slog.Warn("memory: fade weak memory files failed", "err", err)
				}
				if err := forgettingCurve.FadeByRetentionPolicy(gw.initCtx, storageDir, memCfg); err != nil {
					slog.Warn("memory: retention policy enforcement failed", "err", err)
				}
			case <-gw.stopCh:
				return
			}
		}
	}()
	slog.Info("memory: forgetting curve and retention policy enabled (file-based)")

	return nil
}
