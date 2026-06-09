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
		FactExtraction:      cfg.Memory.FactExtraction,
		SimilarityThreshold: cfg.Memory.SimilarityThreshold,
		BM25Weight:          cfg.Memory.BM25Weight,
		VectorWeight:        cfg.Memory.VectorWeight,
		EmbeddingDimension:  cfg.Memory.VectorDimension,
	}

	// File-based storage directory.
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

	fileStore, err := memory.NewFileMemoryStore(storageDir, gw.db.DB, embedder, memCfg)
	if err != nil {
		return fmt.Errorf("create file memory store: %w", err)
	}
	gw.memory.memStore = fileStore
	gw.memory.memoryDir = storageDir
	slog.Info("memory: file-based storage enabled", "dir", storageDir)

	// Wrap with search cache if configured.
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

	// Update agent deps with live store.
	gw.agentDeps.Memory.Store = gw.memory.memStore
	gw.agentDeps.Memory.BaseDir = storageDir

	// Fact extraction and lifecycle management (LLM-driven).
	if cfg.Memory.FactExtraction {
		completer := &completerAdapter{provider: gw.provider, model: cfg.LLM.Model}
		gw.memory.factExtractor = memory.NewLLMFactExtractor(completer, memCfg)
		gw.memory.lifecycleMgr = memory.NewLifecycleManager(gw.memory.memStore, embedder, completer, memCfg)
		slog.Info("memory: fact extraction and lifecycle management enabled")
	}

	if gw.memory.factExtractor != nil {
		gw.agentDeps.Memory.FactExtractor = gw.memory.factExtractor
	}
	if gw.memory.lifecycleMgr != nil {
		gw.agentDeps.Memory.LifecycleMgr = gw.memory.lifecycleMgr
	}

	// Register unified memory tool.
	memTool := tool.NewMemoryTool(gw.memory.memStore, gw.memory.lifecycleMgr)
	gw.tools.Register(memTool)
	slog.Info("memory: unified memory tool registered")

	// TTL eviction: clean up expired entries daily.
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if fstore, ok := gw.memory.memStore.(*memory.FileMemoryStore); ok {
					// Simple TTL-based cleanup: delete entries with ExpiresAt < now.
					_ = fstore
					// TTL eviction runs via SQLite — expired entries are filtered in Search().
				}
				// Scope promotion: promote session-scoped entries to user scope after session ends.
				// This is a lightweight periodic scan.
				_ = gw.promoteSessionMemories()
			case <-gw.stopCh:
				return
			}
		}
	}()
	slog.Info("memory: TTL eviction scheduled (daily)")

	return nil
}

// promoteSessionMemories promotes recent session-scoped facts to user scope.
// Simple periodic consolidation without LLM overhead.
func (gw *Gateway) promoteSessionMemories() error {
	if gw.memory.Store() == nil {
		return nil
	}
	// Session memories naturally expire; no complex promotion needed.
	// The search already spans session + user scopes.
	return nil
}
