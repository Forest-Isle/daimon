package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// LifecycleDecision represents the action to take for a new fact candidate.
type LifecycleDecision struct {
	Action        MemoryAction
	TargetID      string   // for UPDATE/DELETE: the existing entry ID
	Reason        string
	ConflictingIDs []string // IDs of conflicting memories
	RelatedTo     string   // ID of related memory for complementary facts
}

// LifecycleManager implements the ADD/UPDATE/DELETE/NOOP decision loop.
// It mirrors the mem0 core design: new fact -> similarity search -> LLM decision -> execute.
type LifecycleManager struct {
	store     Store
	embedder  EmbeddingProvider
	completer Completer
	cfg       MemoryConfig
}

// NewLifecycleManager creates a new LifecycleManager.
func NewLifecycleManager(store Store, embedder EmbeddingProvider, completer Completer, cfg MemoryConfig) *LifecycleManager {
	return &LifecycleManager{
		store:     store,
		embedder:  embedder,
		completer: completer,
		cfg:       cfg,
	}
}

const lifecycleSystemPrompt = `You are a memory lifecycle manager. Given a new fact candidate and existing similar memories, decide what action to take.

Actions:
- ADD: the new fact is novel and should be stored
- UPDATE: the new fact supersedes an existing memory (provide target_id)
- DELETE: the new fact invalidates an existing memory (provide target_id)
- NOOP: the fact is already captured; do nothing

Conflict Detection:
- Check if new fact contradicts existing memories (mark conflicting_ids)
- Check if new fact updates/supersedes existing memories (temporal supersession)
- Check if new fact complements existing memories (mark related_to)
- Check if new fact duplicates existing memories (NOOP)

Output ONLY JSON: {"action": "ADD|UPDATE|DELETE|NOOP", "target_id": "<id or empty>", "reason": "<brief reason>", "conflicting_ids": ["<id1>", "<id2>"], "related_to": "<id or empty>"}`

// Process decides and executes the lifecycle action for a single extracted fact.
func (lm *LifecycleManager) Process(ctx context.Context, fact ExtractedFact, sessionID, userID string, scope MemoryScope) error {
	threshold := lm.cfg.SimilarityThreshold
	if threshold <= 0 {
		threshold = 0.85
	}

	// Search for similar existing facts.
	similar, err := lm.store.Search(ctx, SearchQuery{
		Text:   fact.Content,
		Limit:  5,
		UserID: userID,
		Scopes: []MemoryScope{scope, ScopeUser},
	})
	if err != nil {
		slog.Warn("lifecycle: similarity search failed", "err", err)
		similar = nil
	}

	// Filter to truly similar results above threshold.
	var candidates []SearchResult
	for _, r := range similar {
		if r.Score >= threshold {
			candidates = append(candidates, r)
		}
	}

	// If no similar facts and LLM not available, just ADD.
	if lm.completer == nil || len(candidates) == 0 {
		return lm.executeAdd(ctx, fact, sessionID, userID, scope)
	}

	// Ask LLM to decide.
	decision, err := lm.decide(ctx, fact, candidates)
	if err != nil {
		slog.Warn("lifecycle: LLM decision failed, defaulting to ADD", "err", err)
		decision = &LifecycleDecision{Action: ActionADD}
	}

	contentPreview := fact.Content
	if len(contentPreview) > 50 {
		contentPreview = contentPreview[:50]
	}
	slog.Info("lifecycle decision",
		"action", decision.Action,
		"fact", contentPreview,
		"reason", decision.Reason,
	)

	switch decision.Action {
	case ActionADD:
		return lm.executeAdd(ctx, fact, sessionID, userID, scope, decision.RelatedTo)
	case ActionUPDATE:
		if decision.TargetID != "" {
			return lm.executeUpdate(ctx, decision.TargetID, fact, sessionID, userID, scope)
		}
		return lm.executeAdd(ctx, fact, sessionID, userID, scope, decision.RelatedTo)
	case ActionDELETE:
		if decision.TargetID != "" {
			return lm.executeDelete(ctx, decision.TargetID)
		}
	case ActionNOOP:
		// Nothing to do.
	}
	return nil
}

// decide calls the LLM to choose ADD/UPDATE/DELETE/NOOP for the given fact and candidates.
func (lm *LifecycleManager) decide(ctx context.Context, fact ExtractedFact, candidates []SearchResult) (*LifecycleDecision, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("NEW FACT: %s\n\nEXISTING SIMILAR MEMORIES:\n", fact.Content))
	for _, c := range candidates {
		sb.WriteString(fmt.Sprintf("- ID: %s, Score: %.3f\n  Content: %s\n",
			c.Entry.ID, c.Score, c.Entry.Content))
	}

	resp, err := lm.completer.Complete(ctx, lifecycleSystemPrompt, sb.String())
	if err != nil {
		return nil, err
	}

	text := strings.TrimSpace(resp)
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		var raw struct {
			Action         string   `json:"action"`
			TargetID       string   `json:"target_id"`
			Reason         string   `json:"reason"`
			ConflictingIDs []string `json:"conflicting_ids"`
			RelatedTo      string   `json:"related_to"`
		}
		if err := json.Unmarshal([]byte(text[start:end+1]), &raw); err == nil {
			return &LifecycleDecision{
				Action:         MemoryAction(raw.Action),
				TargetID:       raw.TargetID,
				Reason:         raw.Reason,
				ConflictingIDs: raw.ConflictingIDs,
				RelatedTo:      raw.RelatedTo,
			}, nil
		}
	}
	return &LifecycleDecision{Action: ActionADD}, nil
}

// executeAdd stores a new fact entry in the memory_facts table.
func (lm *LifecycleManager) executeAdd(ctx context.Context, fact ExtractedFact, sessionID, userID string, scope MemoryScope, relatedTo string) error {
	now := time.Now()
	metadata := map[string]string{
		"category": fact.Category,
		"source":   "fact_extraction",
	}
	if relatedTo != "" {
		metadata["related_to"] = relatedTo
	}

	return lm.store.SaveFact(ctx, Entry{
		SessionID: sessionID,
		UserID:    userID,
		Scope:     scope,
		Content:   fact.Content,
		Metadata:  metadata,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

// executeUpdate archives old file and creates new file with updated content.
func (lm *LifecycleManager) executeUpdate(ctx context.Context, targetID string, fact ExtractedFact, sessionID, userID string, scope MemoryScope) error {
	// Delete (archive) old entry
	if err := lm.store.DeleteFact(ctx, targetID); err != nil {
		return fmt.Errorf("archive old entry: %w", err)
	}

	// Create new entry
	now := time.Now()
	return lm.store.SaveFact(ctx, Entry{
		SessionID: sessionID,
		UserID:    userID,
		Scope:     scope,
		Content:   fact.Content,
		Metadata: map[string]string{
			"category":   fact.Category,
			"source":     "fact_extraction",
			"updated_from": targetID,
		},
		CreatedAt: now,
		UpdatedAt: now,
	})
}

// executeDelete moves file to archived/ subdirectory.
func (lm *LifecycleManager) executeDelete(ctx context.Context, targetID string) error {
	return lm.store.DeleteFact(ctx, targetID)
}
