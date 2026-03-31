package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
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

// GraphSyncer is an optional interface for syncing memory events to the knowledge graph.
type GraphSyncer interface {
	SyncOnAdd(ctx context.Context, factID, content string) error
	SyncOnUpdate(ctx context.Context, oldFactID, newFactID, content string) error
	SyncOnDelete(ctx context.Context, factID string) error
}

// LifecycleManager implements the ADD/UPDATE/DELETE/NOOP decision loop.
// It mirrors the mem0 core design: new fact -> similarity search -> LLM decision -> execute.
type LifecycleManager struct {
	store     Store
	embedder  EmbeddingProvider
	completer Completer
	cfg       MemoryConfig
	reflector *ReflectionTracker
	graphSync GraphSyncer
}

// NewLifecycleManager creates a new LifecycleManager.
// The reflector parameter is optional (can be nil) — when provided, each processed
// fact is tracked for reflection trigger evaluation.
func NewLifecycleManager(store Store, embedder EmbeddingProvider, completer Completer, cfg MemoryConfig, reflector *ReflectionTracker) *LifecycleManager {
	return &LifecycleManager{
		store:     store,
		embedder:  embedder,
		completer: completer,
		cfg:       cfg,
		reflector: reflector,
	}
}

// SetGraphSync attaches an optional graph syncer to the lifecycle manager.
// This is called after construction because the graph may be initialized after
// the lifecycle manager is created.
func (lm *LifecycleManager) SetGraphSync(gs GraphSyncer) {
	lm.graphSync = gs
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

	// Determine lifecycle decision.
	var decision *LifecycleDecision

	if lm.completer == nil || len(candidates) == 0 {
		// No similar facts or no LLM — default to ADD.
		decision = &LifecycleDecision{Action: ActionADD}
	} else {
		// Ask LLM to decide.
		decision, err = lm.decide(ctx, fact, candidates)
		if err != nil {
			slog.Warn("lifecycle: LLM decision failed, defaulting to ADD", "err", err)
			decision = &LifecycleDecision{Action: ActionADD}
		}
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

	// Execute the lifecycle action.
	var execErr error
	switch decision.Action {
	case ActionADD:
		execErr = lm.executeAdd(ctx, fact, sessionID, userID, scope, decision.RelatedTo)
	case ActionUPDATE:
		if decision.TargetID != "" {
			execErr = lm.executeUpdate(ctx, decision.TargetID, fact, sessionID, userID, scope)
		} else {
			execErr = lm.executeAdd(ctx, fact, sessionID, userID, scope, decision.RelatedTo)
		}
	case ActionDELETE:
		if decision.TargetID != "" {
			execErr = lm.executeDelete(ctx, decision.TargetID)
		}
	case ActionNOOP:
		// Nothing to do.
	}

	// Trigger reflection check if reflector is available
	if lm.reflector != nil && decision.Action != ActionNOOP {
		trackID := fmt.Sprintf("fact_%d", time.Now().UnixNano())
		if err := lm.reflector.Track(ctx, trackID, fact.Content, userID); err != nil {
			slog.Warn("lifecycle: reflection tracking failed", "err", err)
		}
	}

	return execErr
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

// executeAdd stores a new fact entry as a Markdown file.
func (lm *LifecycleManager) executeAdd(ctx context.Context, fact ExtractedFact, sessionID, userID string, scope MemoryScope, relatedTo string) error {
	now := time.Now()
	// Generate a predictable ID so we can pass it to both store.Save and graphSync.
	factID := fmt.Sprintf("fact_%d", now.UnixNano())

	metadata := map[string]string{
		"category": fact.Category,
		"source":   "fact_extraction",
	}
	if relatedTo != "" {
		metadata["related_to"] = relatedTo
	}
	metadata["type"] = fact.Type
	if fact.Importance > 0 {
		metadata["importance"] = strconv.Itoa(fact.Importance)
	}
	if fact.Emotion != "" {
		metadata["emotion"] = fact.Emotion
	}
	if fact.Sensitivity != "" {
		metadata["sensitivity"] = fact.Sensitivity
	}

	if err := lm.store.Save(ctx, Entry{
		ID:        factID,
		SessionID: sessionID,
		UserID:    userID,
		Scope:     scope,
		Content:   fact.Content,
		Metadata:  metadata,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return err
	}

	// Sync to knowledge graph if available
	if lm.graphSync != nil {
		if err := lm.graphSync.SyncOnAdd(ctx, factID, fact.Content); err != nil {
			slog.Warn("lifecycle: graph sync on add failed", "id", factID, "err", err)
		}
	}

	return nil
}

// executeUpdate archives old file and creates new file with updated content.
func (lm *LifecycleManager) executeUpdate(ctx context.Context, targetID string, fact ExtractedFact, sessionID, userID string, scope MemoryScope) error {
	// Delete (archive) old entry
	if err := lm.store.Delete(ctx, targetID); err != nil {
		return fmt.Errorf("archive old entry: %w", err)
	}

	// Create new entry with a predictable ID
	now := time.Now()
	newFactID := fmt.Sprintf("fact_%d", now.UnixNano())

	metadata := map[string]string{
		"category":     fact.Category,
		"source":       "fact_extraction",
		"updated_from": targetID,
	}
	metadata["type"] = fact.Type
	if fact.Importance > 0 {
		metadata["importance"] = strconv.Itoa(fact.Importance)
	}
	if fact.Emotion != "" {
		metadata["emotion"] = fact.Emotion
	}
	if fact.Sensitivity != "" {
		metadata["sensitivity"] = fact.Sensitivity
	}

	if err := lm.store.Save(ctx, Entry{
		ID:        newFactID,
		SessionID: sessionID,
		UserID:    userID,
		Scope:     scope,
		Content:   fact.Content,
		Metadata:  metadata,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return err
	}

	// Sync to knowledge graph if available
	if lm.graphSync != nil {
		if err := lm.graphSync.SyncOnUpdate(ctx, targetID, newFactID, fact.Content); err != nil {
			slog.Warn("lifecycle: graph sync on update failed", "old_id", targetID, "new_id", newFactID, "err", err)
		}
	}

	return nil
}

// executeDelete moves file to archived/ subdirectory.
func (lm *LifecycleManager) executeDelete(ctx context.Context, targetID string) error {
	if err := lm.store.Delete(ctx, targetID); err != nil {
		return err
	}

	// Sync to knowledge graph if available
	if lm.graphSync != nil {
		if err := lm.graphSync.SyncOnDelete(ctx, targetID); err != nil {
			slog.Warn("lifecycle: graph sync on delete failed", "id", targetID, "err", err)
		}
	}

	return nil
}
