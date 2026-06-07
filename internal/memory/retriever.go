package memory

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// UnifiedRetriever wraps memory and procedural stores.
type UnifiedRetriever struct {
	memStore      Store
	procedural    *ProceduralStore
	fusionWeights *FusionWeights
	embedder      EmbeddingProvider
}

func NewUnifiedRetriever(
	memStore Store,
	procedural *ProceduralStore,
	embedder EmbeddingProvider,
) *UnifiedRetriever {
	return &UnifiedRetriever{
		memStore:      memStore,
		procedural:    procedural,
		fusionWeights: DefaultFusionWeights(),
		embedder:      embedder,
	}
}

// SetFusionWeights updates the fusion weights. If nil, uses defaults.
func (ur *UnifiedRetriever) SetFusionWeights(w *FusionWeights) {
	if w == nil {
		ur.fusionWeights = DefaultFusionWeights()
		return
	}
	ur.fusionWeights = w
}

// GetProcedural returns the procedural store backing this retriever.
func (ur *UnifiedRetriever) GetProcedural() *ProceduralStore {
	if ur == nil {
		return nil
	}
	return ur.procedural
}

// Search performs a unified search across memory and procedural sources.
func (ur *UnifiedRetriever) Search(
	ctx context.Context,
	query string,
	opts SearchOptions,
) ([]*UnifiedMemory, error) {
	if opts.Limit == 0 {
		opts.Limit = 5
	}

	var (
		wg               sync.WaitGroup
		memories         []*UnifiedMemory
		proceduralResult []*UnifiedMemory
		firstErr         error
		errMu            sync.Mutex
	)

	recordErr := func(source string, err error) {
		if err == nil {
			return
		}
		slog.Warn("memory: source search failed", "source", source, "err", err)
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
	}

	wg.Add(2)

	go func() {
		defer wg.Done()
		if ur.memStore == nil {
			return
		}
		// Embed the query for hybrid BM25+vector search
		var queryEmbedding []float32
		if ur.embedder != nil {
			emb, err := ur.embedder.Embed(ctx, query)
			if err != nil {
				slog.Debug("memory: failed to embed query, falling back to text-only", "err", err)
			} else {
				queryEmbedding = emb
			}
		}
		results, err := ur.memStore.Search(ctx, SearchQuery{
			Text:         query,
			Embedding:    queryEmbedding,
			Limit:        opts.Limit,
			SessionID:    opts.SessionID,
			UserID:       opts.UserID,
			Scopes:       []MemoryScope{ScopeSession, ScopeUser},
			ExcludeTypes: []string{"profile", "procedural"},
		})
		if err != nil {
			recordErr("memory", err)
			return
		}
		memories = make([]*UnifiedMemory, 0, len(results))
		for _, result := range results {
			memories = append(memories, &UnifiedMemory{
				ID:      result.Entry.ID,
				Type:    Episodic,
				Content: result.Entry.Content,
				Score:   result.Score,
				Source:  "memory",
			})
		}
	}()

	go func() {
		defer wg.Done()
		if ur.procedural == nil {
			return
		}
		records, err := ur.procedural.FindSimilar(ctx, query, 3)
		if err != nil {
			recordErr("procedural", err)
			return
		}
		proceduralResult = make([]*UnifiedMemory, 0, len(records))
		for i, record := range records {
			if record == nil {
				continue
			}
			proceduralResult = append(proceduralResult, &UnifiedMemory{
				ID:       fmt.Sprintf("procedural_%d", i),
				Type:     Procedural,
				Content:  fmt.Sprintf("Strategy: %s using tools: %s", record.TaskPattern, strings.Join(record.ToolSequence, ", ")),
				Score:    record.SuccessRate,
				Source:   "procedural",
				Strategy: record,
			})
		}
	}()

	wg.Wait()

	all := make([]*UnifiedMemory, 0, len(memories)+len(proceduralResult))
	all = append(all, applySourceWeight(memories, ur.fusionWeights.MemoryWeight)...)
	all = append(all, applySourceWeight(proceduralResult, ur.fusionWeights.ProceduralWeight)...)

	sort.Slice(all, func(i, j int) bool {
		if all[i].Score == all[j].Score {
			return all[i].Content < all[j].Content
		}
		return all[i].Score > all[j].Score
	})

	all = dedupeByContentSimilarity(all)

	maxResults := opts.Limit * 2
	if maxResults < 20 {
		maxResults = 20
	}
	if len(all) > maxResults {
		all = all[:maxResults]
	}

	if len(all) == 0 {
		return nil, firstErr
	}
	return all, nil
}

func applySourceWeight(items []*UnifiedMemory, weight float64) []*UnifiedMemory {
	if len(items) == 0 {
		return nil
	}

	minScore := items[0].Score
	maxScore := items[0].Score
	for _, item := range items[1:] {
		if item.Score < minScore {
			minScore = item.Score
		}
		if item.Score > maxScore {
			maxScore = item.Score
		}
	}

	result := make([]*UnifiedMemory, 0, len(items))
	for rank, item := range items {
		cloned := *item
		rrf := 1.0 / float64(rank+1+60)
		if maxScore == minScore {
			cloned.Score = (1.0 + rrf) * weight
		} else {
			normalized := (item.Score - minScore) / (maxScore - minScore)
			cloned.Score = (normalized + rrf) * weight
		}
		result = append(result, &cloned)
	}

	return result
}

func dedupeByContentSimilarity(items []*UnifiedMemory) []*UnifiedMemory {
	if len(items) == 0 {
		return nil
	}

	result := make([]*UnifiedMemory, 0, len(items))
	for _, item := range items {
		duplicate := false
		for _, existing := range result {
			if contentSimilarity(existing.Content, item.Content) > 0.8 {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result = append(result, item)
		}
	}
	return result
}

func contentSimilarity(a, b string) float64 {
	a = strings.TrimSpace(strings.ToLower(a))
	b = strings.TrimSpace(strings.ToLower(b))
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}

	shorter := a
	longer := b
	if len(shorter) > len(longer) {
		shorter, longer = longer, shorter
	}

	matches := 0
	for _, r := range shorter {
		if strings.ContainsRune(longer, r) {
			matches++
		}
	}

	return float64(matches) / float64(len([]rune(shorter)))
}
