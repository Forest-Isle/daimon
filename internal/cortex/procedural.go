package cortex

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/memory"
)

// ProceduralStore records and retrieves successful task execution strategies.
type ProceduralStore struct {
	store    memory.Store
	embedder memory.EmbeddingProvider
}

func NewProceduralStore(store memory.Store, embedder memory.EmbeddingProvider) *ProceduralStore {
	return &ProceduralStore{store: store, embedder: embedder}
}

// RecordStrategy stores a successful task execution pattern.
// Called after REFLECT confirms task success.
func (ps *ProceduralStore) RecordStrategy(
	ctx context.Context,
	taskDescription string,
	toolsUsed []string,
	contextHints []string,
	success bool,
	sessionID string,
	userID string,
) error {
	if ps == nil || ps.store == nil || !success {
		return nil
	}

	record := &StrategyRecord{
		TaskPattern:  taskDescription,
		ToolSequence: append([]string(nil), toolsUsed...),
		ContextHints: append([]string(nil), contextHints...),
		SuccessRate:  1.0,
		LastUsed:     time.Now(),
	}

	content, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal strategy record: %w", err)
	}

	entry := memory.Entry{
		ID:        fmt.Sprintf("strat_%d", time.Now().UnixNano()),
		SessionID: sessionID,
		UserID:    userID,
		Scope:     memory.ScopeUser,
		Content:   string(content),
		Metadata: map[string]string{
			"type":     "procedural",
			"category": "strategy",
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if ps.embedder != nil {
		if embedding, embErr := ps.embedder.Embed(ctx, taskDescription); embErr != nil {
			slog.Debug("cortex: procedural embedding failed", "err", embErr)
		} else {
			entry.Embedding = embedding
		}
	}

	if err := ps.store.Save(ctx, entry); err != nil {
		return fmt.Errorf("save procedural strategy: %w", err)
	}

	slog.Debug("cortex: recorded procedural strategy",
		"task", taskDescription,
		"tools", len(toolsUsed),
		"user_id", userID,
		"session_id", sessionID,
	)
	return nil
}

// FindSimilar finds procedural memories similar to the given task description.
func (ps *ProceduralStore) FindSimilar(
	ctx context.Context,
	taskDescription string,
	limit int,
) ([]*StrategyRecord, error) {
	if ps == nil || ps.store == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 3
	}

	results, err := ps.store.Search(ctx, memory.SearchQuery{
		Text:       taskDescription,
		Limit:      limit,
		TypeFilter: "procedural",
		Scopes:     []memory.MemoryScope{memory.ScopeUser},
	})
	if err != nil {
		return nil, fmt.Errorf("search procedural strategies: %w", err)
	}

	records := make([]*StrategyRecord, 0, len(results))
	for _, result := range results {
		var record StrategyRecord
		if err := json.Unmarshal([]byte(result.Entry.Content), &record); err != nil {
			slog.Debug("cortex: skip invalid procedural record", "memory_id", result.Entry.ID, "err", err)
			continue
		}
		records = append(records, &record)
	}

	return records, nil
}
