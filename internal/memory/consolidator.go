package memory

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Consolidator promotes session-scoped facts to user scope after a session ends.
// It runs periodically in the background.
type Consolidator struct {
	store    Store
	interval time.Duration
	done     chan struct{}
}

// NewConsolidator creates a Consolidator with the given interval.
func NewConsolidator(store Store, interval time.Duration) *Consolidator {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	return &Consolidator{
		store:    store,
		interval: interval,
		done:     make(chan struct{}),
	}
}

// Start begins the background consolidation loop.
func (c *Consolidator) Start(ctx context.Context) {
	go c.loop(ctx)
}

// Stop signals the consolidation loop to exit.
func (c *Consolidator) Stop() {
	close(c.done)
}

func (c *Consolidator) loop(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := c.consolidate(ctx); err != nil {
				slog.Warn("consolidator: consolidation failed", "err", err)
			}
		case <-c.done:
			return
		case <-ctx.Done():
			return
		}
	}
}

// consolidate promotes high-value session facts to user scope.
// For MVP: simply copies session facts that are older than the interval to user scope.
func (c *Consolidator) consolidate(ctx context.Context) error {
	slog.Info("consolidator: running session->user consolidation")

	// List all session facts
	facts, err := c.store.ListByScope(ctx, ScopeSession, "")
	if err != nil {
		return fmt.Errorf("list session facts: %w", err)
	}

	promoted := 0
	for _, fact := range facts {
		// Skip recently created facts (less than interval old)
		if time.Since(fact.CreatedAt) < c.interval {
			continue
		}
		if fact.UserID == "" {
			continue // can't promote without user ID
		}

		// Promote to user scope by saving a new user-scoped fact
		promotedFact := fact
		promotedFact.ID = fmt.Sprintf("fact_promoted_%d", time.Now().UnixNano())
		promotedFact.Scope = ScopeUser
		promotedFact.Version = 1
		now := time.Now()
		promotedFact.CreatedAt = now
		promotedFact.UpdatedAt = now
		if promotedFact.Metadata == nil {
			promotedFact.Metadata = make(map[string]string)
		}
		promotedFact.Metadata["promoted_from"] = fact.ID
		promotedFact.Metadata["promoted_at"] = now.Format(time.RFC3339)

		if err := c.store.SaveFact(ctx, promotedFact); err != nil {
			slog.Warn("consolidator: failed to promote fact", "id", fact.ID, "err", err)
			continue
		}
		promoted++
	}

	slog.Info("consolidator: done", "promoted", promoted, "total_session_facts", len(facts))
	return nil
}
