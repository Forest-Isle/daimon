package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/punkopunko/ironclaw/internal/knowledge"
	"github.com/punkopunko/ironclaw/internal/knowledge/graph"
	"github.com/punkopunko/ironclaw/internal/memory"
	"github.com/punkopunko/ironclaw/internal/session"
)

// Perceiver implements the PERCEIVE phase: parse goal, retrieve memories, assess complexity.
type Perceiver struct {
	memStore memory.Store
	searcher knowledge.Searcher // optional knowledge searcher (KB or HybridRetriever)
	graph    graph.Graph        // optional knowledge graph
}

// NewPerceiver creates a new Perceiver.
func NewPerceiver(memStore memory.Store) *Perceiver {
	return &Perceiver{memStore: memStore}
}

// SetKnowledgeSearcher injects an optional knowledge searcher for retrieval during perception.
func (p *Perceiver) SetKnowledgeSearcher(s knowledge.Searcher) {
	p.searcher = s
}

// SetKnowledgeGraph injects an optional knowledge graph for entity-based retrieval.
func (p *Perceiver) SetKnowledgeGraph(g graph.Graph) {
	p.graph = g
}

// complexityKeywords trigger moderate or complex classification.
var complexKeywords = []string{
	"write", "create", "build", "implement", "develop", "generate", "analyze",
	"research", "search", "find", "download", "install", "run", "execute",
	"deploy", "test", "fix", "debug", "refactor", "update", "delete", "remove",
	"configure", "setup", "migrate", "convert", "transform", "compare",
}

var toolTriggers = []string{
	"bash", "shell", "command", "script", "file", "read", "http", "fetch",
	"request", "api", "url", "browse", "open", "save", "edit",
}

// Run executes the PERCEIVE phase. No LLM calls — pure local heuristics.
func (p *Perceiver) Run(ctx context.Context, sess *session.Session, userMsg, userID string) (*CognitiveState, error) {
	lower := strings.ToLower(userMsg)
	words := strings.Fields(lower)

	complexity := assessComplexity(lower, words)

	// Memory retrieval — include user and session scopes for richer context.
	var memories []memory.SearchResult
	if p.memStore != nil {
		var err error
		memories, err = p.memStore.Search(ctx, memory.SearchQuery{
			Text:   userMsg,
			Limit:  5,
			UserID: userID,
			Scopes: []memory.MemoryScope{memory.ScopeSession, memory.ScopeUser},
		})
		if err != nil {
			slog.Warn("perceive: memory search failed", "err", err)
		}
	}

	// Knowledge base retrieval — fetch relevant document chunks.
	var knowledgeContext []string
	if p.searcher != nil {
		kResults, err := p.searcher.Search(ctx, knowledge.KnowledgeQuery{
			Text:  userMsg,
			Limit: 5,
		})
		if err != nil {
			slog.Warn("perceive: knowledge search failed", "err", err)
		} else {
			for _, r := range kResults {
				knowledgeContext = append(knowledgeContext, r.Chunk.Content)
			}
		}
	}

	// Knowledge graph retrieval — find related entities.
	var graphContext []string
	if p.graph != nil {
		graphContext = p.queryGraph(ctx, userMsg)
	}

	// Build recent history for context (used in PLAN prompt).
	recentHistory := BuildMessages(sess)

	state := &CognitiveState{
		SessionID:   sess.ID,
		UserID:      userID,
		UserMessage: userMsg,
		Goal: Goal{
			Raw:        userMsg,
			Intent:     extractIntent(lower),
			Complexity: complexity,
		},
		RelevantMemories: memories,
		RecentHistory:    recentHistory,
		KnowledgeContext: knowledgeContext,
		GraphContext:     graphContext,
	}

	slog.Info("perceive complete",
		"complexity", complexity,
		"memories", len(memories),
		"knowledge_snippets", len(knowledgeContext),
		"graph_relations", len(graphContext),
		"history_msgs", len(recentHistory),
	)

	return state, nil
}

// assessComplexity uses keyword/length heuristics to classify request complexity.
func assessComplexity(lower string, words []string) TaskComplexity {
	wordCount := len(words)

	// Very short messages without action keywords are simple
	if wordCount <= 5 {
		hasActionKeyword := false
		for _, kw := range complexKeywords {
			if strings.Contains(lower, kw) {
				hasActionKeyword = true
				break
			}
		}
		if !hasActionKeyword {
			return ComplexitySimple
		}
	}

	// Tool triggers indicate at least moderate complexity
	toolTriggerCount := 0
	for _, trig := range toolTriggers {
		if strings.Contains(lower, trig) {
			toolTriggerCount++
		}
	}

	complexKwCount := 0
	for _, kw := range complexKeywords {
		if strings.Contains(lower, kw) {
			complexKwCount++
		}
	}

	// Multiple tool triggers or action keywords = complex
	if toolTriggerCount >= 2 || complexKwCount >= 3 || wordCount > 40 {
		return ComplexityComplex
	}

	// Any tool trigger or action keyword = moderate
	if toolTriggerCount >= 1 || complexKwCount >= 1 {
		return ComplexityModerate
	}

	return ComplexitySimple
}

// extractIntent returns a brief description of the user's apparent intent.
func extractIntent(lower string) string {
	for _, kw := range complexKeywords {
		if strings.Contains(lower, kw) {
			return kw
		}
	}
	return "query"
}

// queryGraph extracts key terms from the user message and queries the knowledge graph.
func (p *Perceiver) queryGraph(ctx context.Context, userMsg string) []string {
	// Extract significant words (>3 chars, not stop words) as candidate entity names.
	words := strings.Fields(userMsg)
	var candidates []string
	for _, w := range words {
		clean := strings.Trim(strings.ToLower(w), ".,!?;:\"'()[]{}。，！？")
		if len(clean) > 3 && !isStopWord(clean) {
			candidates = append(candidates, clean)
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	triples, err := graph.SearchRelated(ctx, p.graph, candidates, 2)
	if err != nil {
		slog.Warn("perceive: graph search failed", "err", err)
		return nil
	}

	var results []string
	for _, t := range triples {
		results = append(results, fmt.Sprintf("%s -[%s]-> %s (%s)", t.Subject.Name, t.Predicate, t.Object.Name, t.Object.Type))
	}
	if len(results) > 10 {
		results = results[:10]
	}
	return results
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

func isStopWord(w string) bool {
	return stopWords[w]
}
