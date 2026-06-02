package memory

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const reflectionSystemPrompt = `You are a memory reflection engine. Given a set of individual facts extracted from conversations, identify patterns, themes, and synthesized insights.

Rules:
1. Look for recurring themes, preferences, or behavioral patterns across the facts.
2. Synthesize higher-level insights that individual facts don't capture alone.
3. Be concise but insightful - capture the "so what" of these facts together.
4. Output a single paragraph of synthesized insight, not a list of facts.
5. Focus on actionable understanding that would help personalize future interactions.

If the facts are too sparse or unrelated to synthesize, output a brief summary of the main themes.`

const l2ReflectionSystemPrompt = `You are a deep reflection engine. Given a set of pattern-level observations about a user, synthesize strategic insights about who they are, what they need, and how to best assist them.

Rules:
1. Look for overarching themes across the pattern observations.
2. Identify the user's role, expertise level, and working style.
3. Suggest how interactions should be adapted for this user.
4. Output 2-3 sentences of strategic insight.
5. Focus on what would fundamentally improve the quality of assistance.`

// ProfilerCallback is notified when reflections complete, enabling profile generation.
type ProfilerCallback interface {
	OnReflectionCreated(ctx context.Context, userID string, level int) error
}

// trackedFact holds a fact ID and its content for reflection.
type trackedFact struct {
	ID      string
	Content string
}

// ReflectionTracker monitors incoming facts and triggers L1/L2 reflections
// based on count thresholds and topic drift detection.
type ReflectionTracker struct {
	store      Store
	completer  Completer
	embedder   EmbeddingProvider
	cfg        MemoryConfig
	db         *sql.DB
	profilerCB ProfilerCallback

	mu                    sync.Mutex
	unreflectedFactCount  int
	unreflectedFacts      []trackedFact
	runningTopicEmbedding []float32
	lastReflectionTopic   []float32
	l1CountSinceLastL2    int
	l1ReflectionIDs       []string
	lastUserID            string
}

// NewReflectionTracker creates a new ReflectionTracker.
func NewReflectionTracker(store Store, completer Completer, embedder EmbeddingProvider, cfg MemoryConfig, db *sql.DB) *ReflectionTracker {
	return &ReflectionTracker{
		store:     store,
		completer: completer,
		embedder:  embedder,
		cfg:       cfg,
		db:        db,
	}
}

// SetProfilerCallback registers a callback invoked after each reflection.
func (rt *ReflectionTracker) SetProfilerCallback(cb ProfilerCallback) {
	rt.profilerCB = cb
}

// reflectionCountThreshold returns the configured threshold or the default of 10.
func (rt *ReflectionTracker) reflectionCountThreshold() int {
	if rt.cfg.ReflectionCountThreshold > 0 {
		return rt.cfg.ReflectionCountThreshold
	}
	return 10
}

// reflectionDriftThreshold returns the configured threshold or the default of 0.7.
func (rt *ReflectionTracker) reflectionDriftThreshold() float64 {
	if rt.cfg.ReflectionDriftThreshold > 0 {
		return rt.cfg.ReflectionDriftThreshold
	}
	return 0.7
}

// reflectionL2Trigger returns the configured L2 trigger count or the default of 5.
func (rt *ReflectionTracker) reflectionL2Trigger() int {
	if rt.cfg.ReflectionL2Trigger > 0 {
		return rt.cfg.ReflectionL2Trigger
	}
	return 5
}

// updateTopicEmbedding applies an exponential moving average (α=0.3) to track
// the running topic centroid across incoming facts.
func (rt *ReflectionTracker) updateTopicEmbedding(factEmbedding []float32) {
	if len(rt.runningTopicEmbedding) == 0 {
		rt.runningTopicEmbedding = make([]float32, len(factEmbedding))
		copy(rt.runningTopicEmbedding, factEmbedding)
		return
	}
	alpha := float32(0.3)
	for i := range rt.runningTopicEmbedding {
		rt.runningTopicEmbedding[i] = alpha*factEmbedding[i] + (1-alpha)*rt.runningTopicEmbedding[i]
	}
}

// shouldTrigger returns true when a reflection should be triggered, based on
// either the fact count threshold or topic drift detection.
func (rt *ReflectionTracker) shouldTrigger() bool {
	if rt.unreflectedFactCount >= rt.reflectionCountThreshold() {
		return true
	}
	if len(rt.lastReflectionTopic) > 0 && len(rt.runningTopicEmbedding) > 0 {
		sim := cosineSimilarity(rt.runningTopicEmbedding, rt.lastReflectionTopic)
		if sim < rt.reflectionDriftThreshold() {
			return true
		}
	}
	return false
}

// Track is called after each fact is processed. It updates internal state and
// triggers reflection when thresholds are met.
func (rt *ReflectionTracker) Track(ctx context.Context, factID string, factContent string, userID string) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.unreflectedFactCount++
	rt.unreflectedFacts = append(rt.unreflectedFacts, trackedFact{ID: factID, Content: factContent})
	rt.lastUserID = userID

	if rt.embedder != nil {
		embedding, err := rt.embedder.Embed(ctx, factContent)
		if err != nil {
			slog.Warn("reflection: failed to embed fact", "fact_id", factID, "error", err)
		} else {
			rt.updateTopicEmbedding(embedding)
		}
	}

	if rt.shouldTrigger() {
		if err := rt.triggerReflection(ctx); err != nil {
			slog.Error("reflection: L1 trigger failed", "error", err)
			return err
		}
	}

	return nil
}

// triggerReflection performs a Level-1 reflection over the accumulated unreflected facts.
func (rt *ReflectionTracker) triggerReflection(ctx context.Context) error {
	if len(rt.unreflectedFacts) == 0 {
		return nil
	}

	// Build the user message from accumulated facts
	var sb strings.Builder
	sb.WriteString("Here are the individual facts to reflect on:\n\n")
	factIDs := make([]string, 0, len(rt.unreflectedFacts))
	for i, f := range rt.unreflectedFacts {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, f.Content)
		factIDs = append(factIDs, f.ID)
	}

	slog.Info("reflection: triggering L1 reflection", "fact_count", len(rt.unreflectedFacts), "user_id", rt.lastUserID)

	// Call the LLM for reflection
	result, err := rt.completer.Complete(ctx, reflectionSystemPrompt, sb.String())
	if err != nil {
		return fmt.Errorf("L1 reflection completion: %w", err)
	}

	// Save as a memory entry
	now := time.Now()
	reflectionID := fmt.Sprintf("refl1_%s_%d", rt.lastUserID, now.UnixMilli())
	entry := Entry{
		ID:        reflectionID,
		UserID:    rt.lastUserID,
		Scope:     ScopeUser,
		Content:   result,
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: map[string]string{
			"type":         "reflection",
			"level":        "1",
			"source_facts": strings.Join(factIDs, ","),
		},
	}

	if rt.embedder != nil {
		embedding, err := rt.embedder.Embed(ctx, result)
		if err == nil {
			entry.Embedding = embedding
		}
	}

	if err := rt.store.Save(ctx, entry); err != nil {
		return fmt.Errorf("save L1 reflection: %w", err)
	}

	// Reset state
	rt.unreflectedFactCount = 0
	rt.unreflectedFacts = nil
	if len(rt.runningTopicEmbedding) > 0 {
		rt.lastReflectionTopic = make([]float32, len(rt.runningTopicEmbedding))
		copy(rt.lastReflectionTopic, rt.runningTopicEmbedding)
	}

	// Track L1 for potential L2
	rt.l1CountSinceLastL2++
	rt.l1ReflectionIDs = append(rt.l1ReflectionIDs, reflectionID)

	slog.Info("reflection: L1 reflection saved", "reflection_id", reflectionID)

	if rt.profilerCB != nil {
		if err := rt.profilerCB.OnReflectionCreated(ctx, rt.lastUserID, 1); err != nil {
			slog.Warn("reflection: profiler callback failed for L1", "err", err)
		}
	}

	if rt.l1CountSinceLastL2 >= rt.reflectionL2Trigger() {
		if err := rt.triggerL2Reflection(ctx); err != nil {
			slog.Error("reflection: L2 trigger failed", "error", err)
			return err
		}
	}

	return nil
}

// triggerL2Reflection performs a Level-2 meta-reflection over accumulated L1 reflections.
func (rt *ReflectionTracker) triggerL2Reflection(ctx context.Context) error {
	if len(rt.l1ReflectionIDs) == 0 {
		return nil
	}

	slog.Info("reflection: triggering L2 reflection", "l1_count", len(rt.l1ReflectionIDs), "user_id", rt.lastUserID)

	// Load L1 reflection contents from store
	var sb strings.Builder
	sb.WriteString("Here are the pattern-level observations to synthesize:\n\n")

	for i, id := range rt.l1ReflectionIDs {
		results, err := rt.store.Search(ctx, SearchQuery{
			Text:   id,
			Limit:  1,
			UserID: rt.lastUserID,
		})
		if err != nil || len(results) == 0 {
			slog.Warn("reflection: could not load L1 reflection", "id", id, "error", err)
			continue
		}
		fmt.Fprintf(&sb, "%d. %s\n", i+1, results[0].Entry.Content)
	}

	result, err := rt.completer.Complete(ctx, l2ReflectionSystemPrompt, sb.String())
	if err != nil {
		return fmt.Errorf("L2 reflection completion: %w", err)
	}

	// Save as a memory entry
	now := time.Now()
	reflectionID := fmt.Sprintf("refl2_%s_%d", rt.lastUserID, now.UnixMilli())
	entry := Entry{
		ID:        reflectionID,
		UserID:    rt.lastUserID,
		Scope:     ScopeUser,
		Content:   result,
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: map[string]string{
			"type":               "reflection",
			"level":              "2",
			"source_reflections": strings.Join(rt.l1ReflectionIDs, ","),
		},
	}

	if rt.embedder != nil {
		embedding, err := rt.embedder.Embed(ctx, result)
		if err == nil {
			entry.Embedding = embedding
		}
	}

	if err := rt.store.Save(ctx, entry); err != nil {
		return fmt.Errorf("save L2 reflection: %w", err)
	}

	// Reset L2 state
	rt.l1CountSinceLastL2 = 0
	rt.l1ReflectionIDs = nil

	slog.Info("reflection: L2 reflection saved", "reflection_id", reflectionID)

	if rt.profilerCB != nil {
		if err := rt.profilerCB.OnReflectionCreated(ctx, rt.lastUserID, 2); err != nil {
			slog.Warn("reflection: profiler callback failed for L2", "err", err)
		}
	}

	return nil
}

// SaveState persists the tracker's state to the database for recovery across restarts.
func (rt *ReflectionTracker) SaveState(ctx context.Context, db *sql.DB) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if rt.lastUserID == "" {
		return nil
	}

	// Serialize fact IDs
	factIDs := make([]string, 0, len(rt.unreflectedFacts))
	for _, f := range rt.unreflectedFacts {
		factIDs = append(factIDs, f.ID)
	}

	var runningBlob, lastBlob []byte
	if len(rt.runningTopicEmbedding) > 0 {
		runningBlob = serializeEmbedding(rt.runningTopicEmbedding)
	}
	if len(rt.lastReflectionTopic) > 0 {
		lastBlob = serializeEmbedding(rt.lastReflectionTopic)
	}

	_, err := db.ExecContext(ctx, `
		INSERT INTO reflection_tracker_state (user_id, unreflected_count, unreflected_fact_ids, running_topic_embedding, last_reflection_topic, l1_count_since_last_l2, l1_reflection_ids, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			unreflected_count = excluded.unreflected_count,
			unreflected_fact_ids = excluded.unreflected_fact_ids,
			running_topic_embedding = excluded.running_topic_embedding,
			last_reflection_topic = excluded.last_reflection_topic,
			l1_count_since_last_l2 = excluded.l1_count_since_last_l2,
			l1_reflection_ids = excluded.l1_reflection_ids,
			updated_at = excluded.updated_at
	`, rt.lastUserID, rt.unreflectedFactCount, strings.Join(factIDs, ","),
		runningBlob, lastBlob, rt.l1CountSinceLastL2,
		strings.Join(rt.l1ReflectionIDs, ","), time.Now())
	if err != nil {
		return fmt.Errorf("save reflection tracker state: %w", err)
	}

	return nil
}

// LoadState restores the tracker's state from the database for the given user.
func (rt *ReflectionTracker) LoadState(ctx context.Context, db *sql.DB, userID string) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	var unreflectedCount int
	var unreflectedFactIDsStr string
	var runningBlob, lastBlob []byte
	var l1Count int
	var l1ReflectionIDsStr string

	err := db.QueryRowContext(ctx, `
		SELECT unreflected_count, unreflected_fact_ids, running_topic_embedding, last_reflection_topic, l1_count_since_last_l2, l1_reflection_ids
		FROM reflection_tracker_state
		WHERE user_id = ?
	`, userID).Scan(&unreflectedCount, &unreflectedFactIDsStr, &runningBlob, &lastBlob, &l1Count, &l1ReflectionIDsStr)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil // No state yet, start fresh
		}
		return fmt.Errorf("load reflection tracker state: %w", err)
	}

	rt.lastUserID = userID
	rt.unreflectedFactCount = unreflectedCount

	// Restore fact IDs (content will be empty since we don't persist it)
	rt.unreflectedFacts = nil
	if unreflectedFactIDsStr != "" {
		for _, id := range strings.Split(unreflectedFactIDsStr, ",") {
			id = strings.TrimSpace(id)
			if id != "" {
				rt.unreflectedFacts = append(rt.unreflectedFacts, trackedFact{ID: id})
			}
		}
	}

	if len(runningBlob) > 0 {
		rt.runningTopicEmbedding = deserializeEmbedding(runningBlob)
	}
	if len(lastBlob) > 0 {
		rt.lastReflectionTopic = deserializeEmbedding(lastBlob)
	}

	rt.l1CountSinceLastL2 = l1Count

	rt.l1ReflectionIDs = nil
	if l1ReflectionIDsStr != "" {
		for _, id := range strings.Split(l1ReflectionIDsStr, ",") {
			id = strings.TrimSpace(id)
			if id != "" {
				rt.l1ReflectionIDs = append(rt.l1ReflectionIDs, id)
			}
		}
	}

	slog.Info("reflection: loaded state", "user_id", userID, "unreflected_count", unreflectedCount, "l1_since_l2", l1Count)

	return nil
}
