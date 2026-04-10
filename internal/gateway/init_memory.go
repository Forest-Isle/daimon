package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

func (gw *Gateway) initMemorySystem() error {
	if !gw.cfg.Memory.Enabled {
		return nil
	}

	var embedder memory.EmbeddingProvider = &memory.NoopEmbedding{}
	if gw.cfg.Memory.OpenAIAPIKey != "" {
		baseEmbedder := memory.NewOpenAIEmbedding(gw.cfg.Memory.OpenAIAPIKey, gw.cfg.Memory.EmbeddingModel)
		embedder = memory.NewCachedEmbedder(baseEmbedder)
		slog.Info("memory: cached embedder enabled")
	}
	memCfg := memory.MemoryConfig{
		FactExtraction:           gw.cfg.Memory.FactExtraction,
		SimilarityThreshold:      gw.cfg.Memory.SimilarityThreshold,
		ConsolidationInterval:    gw.cfg.Memory.ConsolidationInterval,
		BM25Weight:               gw.cfg.Memory.BM25Weight,
		VectorWeight:             gw.cfg.Memory.VectorWeight,
		EnableVSS:                gw.cfg.Memory.EnableVSS,
		VectorDimension:          gw.cfg.Memory.VectorDimension,
		EnableSearchCache:        gw.cfg.Memory.EnableSearchCache,
		SearchCacheSize:          gw.cfg.Memory.SearchCacheSize,
		SearchCacheTTL:           gw.cfg.Memory.SearchCacheTTL,
		ReflectionCountThreshold: gw.cfg.Memory.ReflectionCountThreshold,
		ReflectionDriftThreshold: gw.cfg.Memory.ReflectionDriftThreshold,
		ReflectionL2Trigger:      gw.cfg.Memory.ReflectionL2Trigger,
	}

	// File-based storage
	storageDir := gw.cfg.Memory.StorageDir
	if storageDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		storageDir = filepath.Join(home, ".IronClaw", "memory")
	}

	// Create file store
	fileStore, err := memory.NewFileMemoryStore(storageDir, gw.db.DB, embedder, memCfg)
	if err != nil {
		return fmt.Errorf("create file memory store: %w", err)
	}
	gw.memStore = fileStore

	slog.Info("memory: file-based storage enabled", "dir", storageDir)

	gw.runtime.SetMemoryStore(gw.memStore)

	// Set memory base dir on runtime for user profile injection
	gw.runtime.SetMemoryBaseDir(storageDir)

	// Initialize incremental compressor
	compressor := memory.NewIncrementalCompressor(storageDir, &completerAdapter{provider: gw.provider, model: gw.cfg.LLM.Model})
	gw.runtime.SetCompressor(compressor)
	slog.Info("memory: incremental compressor enabled")

	// Initialize forgetting curve manager
	forgettingCurve := memory.NewForgettingCurveManager(gw.db)

	if gw.cfg.Memory.FactExtraction {
		completer := &completerAdapter{provider: gw.provider, model: gw.cfg.LLM.Model}
		gw.factExtractor = memory.NewLLMFactExtractor(completer, memCfg)

		// Create reflection tracker for automatic L1/L2 reflections
		reflector := memory.NewReflectionTracker(gw.memStore, completer, embedder, memCfg, gw.db.DB)
		slog.Info("memory: reflection tracker enabled")

		gw.lifecycleMgr = memory.NewLifecycleManager(gw.memStore, embedder, completer, memCfg, reflector)
		gw.lifecycleMgr.SetAuditLogger(memory.NewAuditLogger(gw.db.DB))

		// Start compactor background task
		compactor := memory.NewCompactor(gw.memStore, completer, gw.db.DB, storageDir, memCfg)
		gw.compactor = compactor
		compactor.Start(context.Background())
		slog.Info("memory: compactor enabled")

		// Create profiler and wire it to the reflection tracker
		profiler := memory.NewProfiler(gw.memStore, completer, gw.db.DB, storageDir, memCfg)
		reflector.SetProfilerCallback(profiler)
		slog.Info("memory: profiler created and wired to reflection tracker")
	}

	// Wire fact extractor and lifecycle manager to simple runtime (if enabled).
	// These may be nil when FactExtraction is disabled; the runtime does nil checks.
	if gw.factExtractor != nil {
		gw.runtime.SetFactExtractor(gw.factExtractor)
	}
	if gw.lifecycleMgr != nil {
		gw.runtime.SetLifecycleManager(gw.lifecycleMgr)
	}

	// Register memory_manage tool
	memTool := tool.NewMemoryManageTool(gw.memStore, gw.db.DB, storageDir)
	gw.tools.Register(memTool)
	slog.Info("memory: memory_manage tool registered")

	// Start consolidator background task (promotes session facts to user scope)
	gw.consolidator = memory.NewConsolidator(gw.memStore, gw.db.DB, storageDir, gw.cfg.Memory.ConsolidationInterval)
	gw.consolidator.Start(context.Background())
	slog.Info("memory: consolidator enabled")

	// Schedule daily retention policy enforcement alongside fade task
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := forgettingCurve.FadeWeakMemoriesFromFiles(context.Background(), storageDir); err != nil {
					slog.Warn("memory: fade weak memory files failed", "err", err)
				}
				if err := forgettingCurve.FadeByRetentionPolicy(context.Background(), storageDir, memCfg); err != nil {
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
