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
	Action         MemoryAction
	TargetID       string
	Reason         string
	ConflictingIDs []string
	RelatedTo      string
}

// LifecycleResult describes the outcome of a single Process() call.
type LifecycleResult struct {
	Action   MemoryAction
	MemoryID string
	Reason   string
}

// MemoryOperationSummary aggregates lifecycle results for user notification.
type MemoryOperationSummary struct {
	Added   int
	Updated int
	Deleted int
}

func (s MemoryOperationSummary) HasChanges() bool {
	return s.Added > 0 || s.Updated > 0 || s.Deleted > 0
}

func (s MemoryOperationSummary) String() string {
	var parts []string
	if s.Added > 0 {
		parts = append(parts, fmt.Sprintf("+%d added", s.Added))
	}
	if s.Updated > 0 {
		parts = append(parts, fmt.Sprintf("%d updated", s.Updated))
	}
	if s.Deleted > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted", s.Deleted))
	}
	return "Memory: " + strings.Join(parts, ", ")
}

// LifecycleManager implements the ADD/UPDATE/DELETE/NOOP decision loop.
// For each fact: similarity search → LLM decision → execute.
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
func (lm *LifecycleManager) Process(ctx context.Context, fact ExtractedFact, sessionID, userID string, scope MemoryScope) (*LifecycleResult, error) {
	threshold := lm.cfg.SimilarityThreshold
	if threshold <= 0 {
		threshold = 0.85
	}

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

	var candidates []SearchResult
	for _, r := range similar {
		if r.Score >= threshold {
			candidates = append(candidates, r)
		}
	}

	var decision *LifecycleDecision
	if lm.completer == nil || len(candidates) == 0 {
		decision = &LifecycleDecision{Action: ActionADD}
	} else {
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
	slog.Info("lifecycle decision", "action", decision.Action, "fact", contentPreview, "reason", decision.Reason)

	var memoryID string
	var execErr error
	switch decision.Action {
	case ActionADD:
		memoryID, execErr = lm.executeAdd(ctx, fact, sessionID, userID, scope, decision.RelatedTo)
	case ActionUPDATE:
		if decision.TargetID != "" {
			memoryID, execErr = lm.executeUpdate(ctx, decision.TargetID, fact, sessionID, userID, scope)
		} else {
			memoryID, execErr = lm.executeAdd(ctx, fact, sessionID, userID, scope, decision.RelatedTo)
		}
	case ActionDELETE:
		if decision.TargetID != "" {
			memoryID = decision.TargetID
			execErr = lm.executeDelete(ctx, decision.TargetID)
		}
	case ActionNOOP:
	}

	if execErr != nil {
		return nil, execErr
	}

	return &LifecycleResult{
		Action:   decision.Action,
		MemoryID: memoryID,
		Reason:   decision.Reason,
	}, nil
}

func (lm *LifecycleManager) decide(ctx context.Context, fact ExtractedFact, candidates []SearchResult) (*LifecycleDecision, error) {
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "NEW FACT: %s\n\nEXISTING SIMILAR MEMORIES:\n", fact.Content)
	for _, c := range candidates {
		_, _ = fmt.Fprintf(&sb, "- ID: %s, Score: %.3f\n  Content: %s\n", c.Entry.ID, c.Score, c.Entry.Content)
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

func (lm *LifecycleManager) executeAdd(ctx context.Context, fact ExtractedFact, sessionID, userID string, scope MemoryScope, relatedTo string) (string, error) {
	now := time.Now()
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
		return "", err
	}
	return factID, nil
}

func (lm *LifecycleManager) executeUpdate(ctx context.Context, targetID string, fact ExtractedFact, sessionID, userID string, scope MemoryScope) (string, error) {
	if err := lm.store.Delete(ctx, targetID); err != nil {
		return "", fmt.Errorf("archive old entry: %w", err)
	}

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
		return "", err
	}
	return newFactID, nil
}

func (lm *LifecycleManager) executeDelete(ctx context.Context, targetID string) error {
	return lm.store.Delete(ctx, targetID)
}
