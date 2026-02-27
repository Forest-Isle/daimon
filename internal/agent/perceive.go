package agent

import (
	"context"
	"log/slog"
	"strings"

	"github.com/punkopunko/ironclaw/internal/memory"
	"github.com/punkopunko/ironclaw/internal/session"
)

// Perceiver implements the PERCEIVE phase: parse goal, retrieve memories, assess complexity.
type Perceiver struct {
	memStore memory.Store
}

// NewPerceiver creates a new Perceiver.
func NewPerceiver(memStore memory.Store) *Perceiver {
	return &Perceiver{memStore: memStore}
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
func (p *Perceiver) Run(ctx context.Context, sess *session.Session, userMsg string) (*CognitiveState, error) {
	lower := strings.ToLower(userMsg)
	words := strings.Fields(lower)

	complexity := assessComplexity(lower, words)

	// Memory retrieval
	var memories []memory.SearchResult
	if p.memStore != nil {
		var err error
		memories, err = p.memStore.Search(ctx, memory.SearchQuery{
			Text:  userMsg,
			Limit: 5,
		})
		if err != nil {
			slog.Warn("perceive: memory search failed", "err", err)
		}
	}

	// Build recent history for context (used in PLAN prompt)
	recentHistory := BuildMessages(sess)

	state := &CognitiveState{
		SessionID:   sess.ID,
		UserMessage: userMsg,
		Goal: Goal{
			Raw:        userMsg,
			Intent:     extractIntent(lower),
			Complexity: complexity,
		},
		RelevantMemories: memories,
		RecentHistory:    recentHistory,
	}

	slog.Info("perceive complete",
		"complexity", complexity,
		"memories", len(memories),
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
