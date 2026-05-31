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

	var embedder memory.EmbeddingProvider = &memory.NoopEmbedding{}
	if gw.cfg.Memory.OpenAIAPIKey != "" {
		baseEmbedder := memory.NewOpenAIEmbeddingWithURL(gw.cfg.Memory.OpenAIAPIKey, gw.cfg.Memory.EmbeddingModel, gw.cfg.Memory.EmbeddingBaseURL)
		embedder = memory.NewCachedEmbedder(baseEmbedder)
		slog.Info("memory: cached embedder enabled")
	}
	gw.memory.embedder = embedder
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

	gw.runtime.SetMemoryStore(gw.memory.memStore)

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
		gw.runtime.SetProfiler(profiler)
		slog.Info("memory: profiler created and wired to reflection tracker")

		if err := profiler.MigrateLegacyProfile(gw.initCtx, "default"); err != nil {
			slog.Warn("memory: legacy profile migration failed", "err", err)
		}
	}

	// Wire fact extractor and lifecycle manager to simple runtime (if enabled).
	// These may be nil when FactExtraction is disabled; the runtime does nil checks.
	if gw.memory.factExtractor != nil {
		gw.runtime.SetFactExtractor(gw.memory.factExtractor)
	}
	if gw.memory.lifecycleMgr != nil {
		gw.runtime.SetLifecycleManager(gw.memory.lifecycleMgr)
	}

	// Register memory_manage tool
	memTool := tool.NewMemoryManageTool(gw.memory.memStore, gw.db.DB, storageDir)
	gw.tools.Register(memTool)
	slog.Info("memory: memory_manage tool registered")

	// Start consolidator background task (promotes session facts to user scope)
	gw.memory.consolidator = memory.NewConsolidator(gw.memory.memStore, gw.db.DB, storageDir, gw.cfg.Memory.ConsolidationInterval)
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
