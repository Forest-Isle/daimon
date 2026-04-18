package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/knowledge"
	"github.com/Forest-Isle/IronClaw/internal/knowledge/graph"
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/session"
)

// Perceiver implements the PERCEIVE phase: parse goal, retrieve memories, assess complexity.
type Perceiver struct {
	memStore    memory.Store
	searcher    knowledge.Searcher     // optional knowledge searcher (KB or HybridRetriever)
	graph       graph.Graph            // optional knowledge graph
	rlPolicy    RLPolicy               // optional RL policy
	scanner     *ProjectContextScanner // optional project context scanner
	gitProvider *GitContextProvider     // optional git state provider
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

// SetRLPolicy injects an optional RL policy.
func (p *Perceiver) SetRLPolicy(policy RLPolicy) {
	p.rlPolicy = policy
}

// SetProjectScanner injects an optional project context scanner.
func (p *Perceiver) SetProjectScanner(s *ProjectContextScanner) {
	p.scanner = s
}

// SetGitProvider injects an optional git context provider.
func (p *Perceiver) SetGitProvider(g *GitContextProvider) {
	p.gitProvider = g
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
			slog.Warn("perceive: memory.md search failed", "err", err)
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
	var queryEntities []string
	if p.graph != nil {
		graphContext, queryEntities = p.queryGraphWithEntities(ctx, userMsg)
	}

	// Graph-expanded retrieval: boost memory scores based on graph connectivity.
	if p.graph != nil && len(memories) > 0 && len(queryEntities) > 0 {
		memories = p.boostByGraphConnectivity(ctx, memories, queryEntities)
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

	if p.scanner != nil {
		cwd, _ := os.Getwd()
		state.ProjectCtx = p.scanner.Scan(cwd)
	}

	if p.gitProvider != nil {
		cwd, _ := os.Getwd()
		state.GitState = p.gitProvider.Collect(cwd)
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

// queryGraphWithEntities extracts key terms from the user message, queries the knowledge
// graph, and returns both the formatted triples and the extracted entity candidates.
func (p *Perceiver) queryGraphWithEntities(ctx context.Context, userMsg string) ([]string, []string) {
	candidates := extractEntityCandidates(userMsg)
	if len(candidates) == 0 {
		return nil, nil
	}

	triples, err := graph.SearchRelated(ctx, p.graph, candidates, 2)
	if err != nil {
		slog.Warn("perceive: graph search failed", "err", err)
		return nil, candidates
	}

	var results []string
	for _, t := range triples {
		results = append(results, fmt.Sprintf("%s -[%s]-> %s (%s)", t.Subject.Name, t.Predicate, t.Object.Name, t.Object.Type))
	}
	if len(results) > 10 {
		results = results[:10]
	}
	return results, candidates
}

// boostByGraphConnectivity boosts memory search result scores based on graph connectivity
// between entities found in memory content and the query entities. For the top-3 results,
// it extracts entity names, checks graph connections to query entities, and multiplies
// the score by (1 + 0.2 * connection_count).
func (p *Perceiver) boostByGraphConnectivity(ctx context.Context, results []memory.SearchResult, queryEntities []string) []memory.SearchResult {
	if p.graph == nil || len(results) == 0 {
		return results
	}

	// Build a set of node IDs for query entities for fast lookup.
	queryNodeIDs := make(map[string]bool)
	for _, qe := range queryEntities {
		nodes, err := p.graph.FindByName(ctx, qe)
		if err != nil {
			continue
		}
		for _, n := range nodes {
			queryNodeIDs[n.ID] = true
		}
	}
	if len(queryNodeIDs) == 0 {
		return results
	}

	// Process top-3 results (or fewer if less available).
	limit := 3
	if len(results) < limit {
		limit = len(results)
	}

	for i := 0; i < limit; i++ {
		content := results[i].Entry.Content
		memEntities := extractEntityCandidates(content)
		if len(memEntities) == 0 {
			continue
		}

		connectionCount := 0
		for _, me := range memEntities {
			nodes, err := p.graph.FindByName(ctx, me)
			if err != nil {
				continue
			}
			for _, node := range nodes {
				// Traverse up to 2 hops from this memory entity.
				triples, err := p.graph.Traverse(ctx, node.ID, 2)
				if err != nil {
					continue
				}
				for _, t := range triples {
					// Check if any connected node matches a query entity node.
					if queryNodeIDs[t.Subject.ID] || queryNodeIDs[t.Object.ID] {
						connectionCount++
					}
				}
			}
		}

		if connectionCount > 0 {
			boost := 1.0 + 0.2*float64(connectionCount)
			results[i].Score *= boost
			slog.Debug("perceive: graph-boosted memory result",
				"memory_id", results[i].Entry.ID,
				"connections", connectionCount,
				"boost", boost,
				"new_score", results[i].Score,
			)
		}
	}

	// Re-sort results by score descending after boosting.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results
}

// extractEntityCandidates extracts significant words (>3 chars, non-stopwords) from text
// as candidate entity names for graph lookup.
func extractEntityCandidates(text string) []string {
	words := strings.Fields(text)
	var candidates []string
	seen := make(map[string]bool)
	for _, w := range words {
		clean := strings.Trim(strings.ToLower(w), ".,!?;:\"'()[]{}。，！？")
		if len(clean) > 3 && !isStopWord(clean) && !seen[clean] {
			seen[clean] = true
			candidates = append(candidates, clean)
		}
	}
	return candidates
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
