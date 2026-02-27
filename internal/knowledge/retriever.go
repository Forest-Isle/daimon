package knowledge

import (
	"context"
	"log/slog"
)

// HybridRetriever combines vector search, BM25, and optional reranking.
type HybridRetriever struct {
	kb       *SQLiteKnowledgeBase
	reranker Reranker
}

// NewHybridRetriever creates a retriever backed by the given knowledge base.
func NewHybridRetriever(kb *SQLiteKnowledgeBase, reranker Reranker) *HybridRetriever {
	if reranker == nil {
		reranker = &NoopReranker{}
	}
	return &HybridRetriever{kb: kb, reranker: reranker}
}

// Search performs hybrid retrieval and optional reranking.
func (h *HybridRetriever) Search(ctx context.Context, query KnowledgeQuery) ([]KnowledgeResult, error) {
	results, err := h.kb.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}

	reranked, err := h.reranker.Rerank(ctx, query.Text, results)
	if err != nil {
		slog.Warn("knowledge: reranker failed, using unranked results", "err", err)
		return results, nil
	}
	return reranked, nil
}
