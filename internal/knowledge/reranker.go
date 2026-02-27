package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Reranker re-orders KnowledgeResults by relevance to a query.
type Reranker interface {
	Rerank(ctx context.Context, query string, results []KnowledgeResult) ([]KnowledgeResult, error)
}

// NoopReranker returns results unchanged.
type NoopReranker struct{}

func (n *NoopReranker) Rerank(_ context.Context, _ string, results []KnowledgeResult) ([]KnowledgeResult, error) {
	return results, nil
}

// LLMReranker uses an LLM to rerank results.
type LLMReranker struct {
	completer Completer
}

// NewLLMReranker creates a new LLM-based reranker.
func NewLLMReranker(completer Completer) *LLMReranker {
	return &LLMReranker{completer: completer}
}

const rerankerSystemPrompt = `You are a relevance reranker. Given a query and a list of documents, output a JSON array of document IDs ordered by relevance (most relevant first).

Output ONLY a JSON array of IDs, e.g.: ["doc_3", "doc_1", "doc_2"]`

// Rerank reorders results by relevance using the LLM.
func (r *LLMReranker) Rerank(ctx context.Context, query string, results []KnowledgeResult) ([]KnowledgeResult, error) {
	if len(results) <= 1 {
		return results, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("QUERY: %s\n\nDOCUMENTS:\n", query))
	for i, res := range results {
		content := res.Chunk.Content
		if len(content) > 300 {
			content = content[:300] + "..."
		}
		sb.WriteString(fmt.Sprintf("[%d] ID: %s\n%s\n\n", i, res.Chunk.ID, content))
	}

	resp, err := r.completer.Complete(ctx, rerankerSystemPrompt, sb.String())
	if err != nil {
		return results, nil // fall back to original order on error
	}

	var orderedIDs []string
	text := strings.TrimSpace(resp)
	if start := strings.Index(text, "["); start >= 0 {
		if end := strings.LastIndex(text, "]"); end > start {
			json.Unmarshal([]byte(text[start:end+1]), &orderedIDs) //nolint:errcheck
		}
	}

	if len(orderedIDs) == 0 {
		return results, nil
	}

	// Build rank map
	rankMap := make(map[string]int, len(orderedIDs))
	for i, id := range orderedIDs {
		rankMap[id] = i
	}

	reranked := make([]KnowledgeResult, len(results))
	copy(reranked, results)
	sort.SliceStable(reranked, func(i, j int) bool {
		ri, iOk := rankMap[reranked[i].Chunk.ID]
		rj, jOk := rankMap[reranked[j].Chunk.ID]
		if !iOk {
			ri = len(orderedIDs) + i
		}
		if !jOk {
			rj = len(orderedIDs) + j
		}
		return ri < rj
	})
	return reranked, nil
}
