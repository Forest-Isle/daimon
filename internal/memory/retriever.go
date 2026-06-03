package memory

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/Forest-Isle/IronClaw/internal/knowledge"
	"github.com/Forest-Isle/IronClaw/internal/knowledge/graph"
)

// UnifiedRetriever wraps memory, knowledge, graph, and procedural stores.
type UnifiedRetriever struct {
	memStore      Store
	kbSearcher    knowledge.Searcher
	graphStore    graph.Graph
	procedural    *ProceduralStore
	fusionWeights *FusionWeights
	embedder      EmbeddingProvider
}

func NewUnifiedRetriever(
	memStore Store,
	kbSearcher knowledge.Searcher,
	graphStore graph.Graph,
	procedural *ProceduralStore,
	embedder EmbeddingProvider,
) *UnifiedRetriever {
	return &UnifiedRetriever{
		memStore:      memStore,
		kbSearcher:    kbSearcher,
		graphStore:    graphStore,
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

// Search performs a unified search across all four sources.
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
		knowledgeResults []*UnifiedMemory
		graphResults     []*UnifiedMemory
		proceduralResult []*UnifiedMemory
		graphEntities    []string
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

	wg.Add(4)

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
		if ur.kbSearcher == nil {
			return
		}
		results, err := ur.kbSearcher.Search(ctx, knowledge.KnowledgeQuery{
			Text:  query,
			Limit: opts.Limit,
		})
		if err != nil {
			recordErr("knowledge", err)
			return
		}
		knowledgeResults = make([]*UnifiedMemory, 0, len(results))
		for _, result := range results {
			knowledgeResults = append(knowledgeResults, &UnifiedMemory{
				ID:      result.Chunk.ID,
				Type:    Semantic,
				Content: result.Chunk.Content,
				Score:   result.Score,
				Source:  "knowledge",
			})
		}
	}()

	go func() {
		defer wg.Done()
		if ur.graphStore == nil {
			return
		}
		candidates := ExtractEntityCandidates(query)
		graphEntities = append(graphEntities, candidates...)
		if len(candidates) == 0 {
			return
		}
		triples, err := graph.SearchRelated(ctx, ur.graphStore, candidates, 2)
		if err != nil {
			recordErr("graph", err)
			return
		}
		graphResults = make([]*UnifiedMemory, 0, len(triples))
		for i, triple := range triples {
			graphEntities = append(graphEntities, strings.ToLower(triple.Subject.Name), strings.ToLower(triple.Object.Name))
			graphResults = append(graphResults, &UnifiedMemory{
				ID:      fmt.Sprintf("graph_%d", i),
				Type:    Semantic,
				Content: fmt.Sprintf("%s -[%s]-> %s (%s)", triple.Subject.Name, triple.Predicate, triple.Object.Name, triple.Object.Type),
				Score:   triple.Weight,
				Source:  "graph",
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

	if len(graphEntities) > 0 && len(memories) > 0 {
		boostMemoriesByGraphConnectivity(memories, graphEntities)
	}

	all := make([]*UnifiedMemory, 0, len(memories)+len(knowledgeResults)+len(graphResults)+len(proceduralResult))
	all = append(all, applySourceWeight(memories, ur.fusionWeights.MemoryWeight)...)
	all = append(all, applySourceWeight(knowledgeResults, ur.fusionWeights.KnowledgeWeight)...)
	all = append(all, applySourceWeight(graphResults, ur.fusionWeights.GraphWeight)...)
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

func boostMemoriesByGraphConnectivity(memories []*UnifiedMemory, graphEntities []string) {
	if len(memories) == 0 || len(graphEntities) == 0 {
		return
	}

	entitySet := make(map[string]struct{}, len(graphEntities))
	for _, entity := range graphEntities {
		entity = strings.TrimSpace(strings.ToLower(entity))
		if entity == "" {
			continue
		}
		entitySet[entity] = struct{}{}
	}

	limit := 3
	if len(memories) < limit {
		limit = len(memories)
	}

	for i := 0; i < limit; i++ {
		matches := 0
		for _, candidate := range ExtractEntityCandidates(memories[i].Content) {
			if _, ok := entitySet[candidate]; ok {
				matches++
			}
		}
		if matches == 0 {
			continue
		}
		memories[i].Score *= 1 + 0.2*float64(matches)
	}
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

// ExtractEntityCandidates extracts candidate entity names from text for graph lookups.
// Detects multi-word proper nouns, CamelCase identifiers, hyphenated terms, and single words (>3 chars).
func ExtractEntityCandidates(text string) []string {
	candidates := make([]string, 0, 32)
	seen := make(map[string]bool)

	// Phase 1: multi-word noun phrase detection — catches proper nouns,
	// CamelCase identifiers, hyphenated terms, and dot-separated names
	// using character-level scanning (no regexp dependency).
	multiWordByScan(text, seen, &candidates)

	// Phase 2: single-word extraction (existing logic, fallback)
	words := strings.Fields(text)
	for _, word := range words {
		clean := strings.Trim(strings.ToLower(word), ".,!?;:\"'()[]{}。，！？")
		if len(clean) <= 3 || isStopWord(clean) || seen[clean] {
			continue
		}
		seen[clean] = true
		candidates = append(candidates, clean)
	}
	return candidates
}

// multiWordByScan detects compound entities in text without regexp.
// It identifies: CamelCase tokens, hyphenated terms, and consecutive
// capitalized word sequences (proper nouns).
func multiWordByScan(text string, seen map[string]bool, candidates *[]string) {
	// Scan for CamelCase and hyphenated tokens
	for i := 0; i < len(text); i++ {
		// CamelCase: starts lowercase, contains uppercase — "reactNative"
		if isLower(text[i]) {
			j := i + 1
			hasUpper := false
			for j < len(text) && (isLetter(text[j]) || isDigit(text[j])) {
				if isUpper(text[j]) {
					hasUpper = true
				}
				j++
			}
			if hasUpper && j-i >= 4 {
				addIfNew(strings.ToLower(text[i:j]), seen, candidates)
			}
			i = j - 1
			continue
		}
		// Consecutive capitalized words: "Visual Studio Code"
		if isUpper(text[i]) {
			start := i
			wordCount := 0
			for i < len(text) {
				// Skip leading non-letters
				for i < len(text) && !isLetter(text[i]) {
					if text[i] == '.' || text[i] == ',' || text[i] == ';' {
						goto endSeq
					}
					i++
				}
				if i >= len(text) || !isUpper(text[i]) {
					break
				}
				j := i
				for j < len(text) && isLower(text[j]) {
					j++
				}
				if j > i {
					wordCount++
				}
				i = j
				// Skip spaces
				for i < len(text) && text[i] == ' ' {
					i++
				}
			}
		endSeq:
			if wordCount >= 2 && i-start >= 5 {
				addIfNew(strings.ToLower(text[start:i]), seen, candidates)
			}
			continue
		}
	}
	// Scan for hyphenated terms: "state-of-the-art"
	for i := 0; i < len(text)-3; i++ {
		if isLetter(text[i]) && text[i+1] == '-' && isLetter(text[i+2]) {
			start := i
			j := i + 3
			for j < len(text) && (isLetter(text[j]) || (text[j] == '-' && j+1 < len(text) && isLetter(text[j+1]))) {
				j++
			}
			if j-start >= 5 {
				addIfNew(strings.ToLower(text[start:j]), seen, candidates)
			}
			i = j - 1
		}
	}
}

func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isLower(c byte) bool {
	return c >= 'a' && c <= 'z'
}

func isUpper(c byte) bool {
	return c >= 'A' && c <= 'Z'
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func addIfNew(s string, seen map[string]bool, candidates *[]string) {
	if len(s) > 3 && !isStopWord(s) && !seen[s] {
		seen[s] = true
		*candidates = append(*candidates, s)
	}
}

var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "but": true,
	"not": true, "you": true, "all": true, "can": true, "had": true,
	"her": true, "was": true, "one": true, "our": true, "out": true,
	"has": true, "have": true, "from": true, "this": true, "that": true,
	"with": true, "what": true, "when": true, "where": true, "which": true,
	"will": true, "would": true, "there": true, "their": true, "about": true,
	"them": true, "then": true, "than": true, "been": true, "some": true,
	"could": true, "other": true, "into": true, "more": true, "very": true,
	"just": true, "also": true, "know": true, "how": true, "please": true,
}

func isStopWord(word string) bool {
	return stopWords[word]
}
