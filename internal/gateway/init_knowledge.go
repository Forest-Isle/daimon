package gateway

import (
	"context"
	"log/slog"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/knowledge"
	"github.com/Forest-Isle/IronClaw/internal/knowledge/graph"
	"github.com/Forest-Isle/IronClaw/internal/memory"
)

func (gw *Gateway) initKnowledgeSystem() error {
	if !gw.featureEnabled("knowledge") {
		return nil
	}

	kbCfg := knowledge.Config{
		ChunkSize:         gw.cfg.Knowledge.ChunkSize,
		ChunkOverlap:      gw.cfg.Knowledge.ChunkOverlap,
		BM25Weight:        gw.cfg.Knowledge.BM25Weight,
		VectorWeight:      gw.cfg.Knowledge.VectorWeight,
		IngestDirs:        gw.cfg.Knowledge.IngestDirs,
		EnableSearchCache: gw.cfg.Knowledge.EnableSearchCache,
		SearchCacheSize:   gw.cfg.Knowledge.SearchCacheSize,
		SearchCacheTTL:    gw.cfg.Knowledge.SearchCacheTTL,
	}
	var kbEmbedder knowledge.EmbeddingProvider
	if gw.cfg.Memory.OpenAIAPIKey != "" {
		kbEmbedder = memory.NewOpenAIEmbedding(gw.cfg.Memory.OpenAIAPIKey, gw.cfg.Memory.EmbeddingModel)
	} else {
		kbEmbedder = &noopKBEmbedder{}
	}
	kb := knowledge.New(gw.db, kbEmbedder, kbCfg)
	gw.kbSearcher = nil

	// Build reranker + hybrid retriever (used as the searcher for perceiver)
	var reranker knowledge.Reranker = &knowledge.NoopReranker{}
	if gw.featureEnabled("reranker") && gw.cfg.Knowledge.Reranker.Provider == "llm" {
		llmCompleter := &completerAdapter{provider: gw.provider, model: gw.cfg.LLM.Model}
		reranker = knowledge.NewLLMReranker(llmCompleter)
	}
	retriever := knowledge.NewHybridRetriever(kb, reranker)
	gw.kbSearcher = retriever

	// Ingest configured directories at startup
	for _, dir := range gw.cfg.Knowledge.IngestDirs {
		if err := kb.GetPipeline().IngestDir(context.Background(), dir); err != nil {
			slog.Warn("gateway: failed to ingest dir", "dir", dir, "err", err)
		}
	}

	if gw.cognitiveAgent != nil {
		gw.cognitiveAgent.SetKnowledgeSearcher(retriever)
	}

	// Knowledge graph (Phase 3)
	if gw.featureEnabled("knowledge_graph") {
		kg := graph.NewSQLiteGraph(gw.db)
		gw.graphStore = kg
		llmCompleter := &completerAdapter{provider: gw.provider, model: gw.cfg.LLM.Model}
		extractor := graph.NewLLMEntityExtractor(kg, llmCompleter)

		// Extract entities from already-ingested chunks in background
		go func() {
			sources, err := kb.Sources(context.Background())
			if err != nil {
				slog.Warn("gateway: failed to list KB sources for graph extraction", "err", err)
				return
			}
			for _, src := range sources {
				results, err := kb.Search(context.Background(), knowledge.KnowledgeQuery{
					Text:       "",
					SourceType: src.SourceType,
					Limit:      50,
				})
				if err != nil {
					continue
				}
				for _, r := range results {
					extractor.Extract(context.Background(), r.Chunk.Content, "kb_chunk", r.Chunk.ID) //nolint:errcheck
				}
			}
			slog.Info("gateway: initial graph entity extraction complete")
		}()

		if gw.cognitiveAgent != nil {
			gw.cognitiveAgent.SetKnowledgeGraph(kg)
			gw.cognitiveAgent.SetEntityExtractor(extractor)
		}

		// Wire GraphSync to lifecycle manager for memory→graph synchronization
		if gw.lifecycleMgr != nil {
			graphSync := graph.NewGraphSync(kg, extractor)
			gw.lifecycleMgr.SetGraphSync(graphSync)
			slog.Info("knowledge graph: memory lifecycle sync enabled")
		}

		// Start graph decay background task
		graphDecay := graph.NewGraphDecayTask(kg, 24*time.Hour)
		gw.graphDecay = graphDecay
		go graphDecay.Start(context.Background())
		slog.Info("knowledge graph: decay task started")

		slog.Info("knowledge graph initialized")
	}

	slog.Info("knowledge base initialized", "ingest_dirs", gw.cfg.Knowledge.IngestDirs)
	return nil
}
