// Package economy is the per-episode cost ledger (DAIMON_BLUEPRINT.md §4.11):
// each episode records the tokens it consumed so the agent can later account for
// what it spent and judge whether an activity class is worth its cost. Token
// counts are stored raw; the dollar figure is computed at report time from a
// model price table (a later increment), so price changes never require
// backfilling. The store is deliberately time-free — callers supply the
// timestamp — so the recording site and tests stay deterministic.
package economy

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// Entry is one episode's token consumption. OccurredAt is epoch seconds (the
// caller's to set; the store never reads the clock). ActivityClass is the routing
// kind the episode ran under (chat, a heart verdict kind, …) for ROI-by-class
// reporting; it may be empty until the threading increment lands.
type Entry struct {
	ID                  string
	EpisodeID           string
	Model               string
	Provider            string
	ActivityClass       string
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int
	OccurredAt          int64
}

// Totals is an aggregate over a set of cost rows.
type Totals struct {
	Episodes            int
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) ensure() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("economy store unavailable")
	}
	return nil
}

// Record inserts one cost row, idempotently per episode. The row id is ALWAYS
// derived from EpisodeID ("cost_"+EpisodeID) when present — overriding any caller
// id — and the insert is INSERT OR IGNORE, so a retried or concurrently
// re-delivered episode records exactly one row (mirroring the world's per-episode
// outcome idempotency, and holding even if a caller supplies an explicit id). An
// episode with no id falls back to a random id (no dedup). Negative token counts
// are clamped to zero so a misreported usage cannot corrupt the ledger's sums.
func (s *Store) Record(ctx context.Context, e Entry) error {
	if err := s.ensure(); err != nil {
		return err
	}
	switch {
	case e.EpisodeID != "":
		e.ID = "cost_" + e.EpisodeID
	case e.ID == "":
		e.ID = "cost_" + uuid.NewString()
	}
	clamp := func(n int) int {
		if n < 0 {
			return 0
		}
		return n
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO costs
			(id, episode_id, model, provider, activity_class,
			 input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, occurred_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.EpisodeID, e.Model, e.Provider, e.ActivityClass,
		clamp(e.InputTokens), clamp(e.OutputTokens), clamp(e.CacheReadTokens), clamp(e.CacheCreationTokens),
		e.OccurredAt,
	)
	if err != nil {
		return fmt.Errorf("economy: record cost: %w", err)
	}
	return nil
}

// TotalSince aggregates all cost rows at or after the given epoch-second cutoff
// (since <= 0 ⇒ all rows). It is the primitive the cost report and tests build on.
func (s *Store) TotalSince(ctx context.Context, since int64) (Totals, error) {
	if err := s.ensure(); err != nil {
		return Totals{}, err
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT COUNT(1),
		       COALESCE(SUM(input_tokens), 0),
		       COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(cache_read_tokens), 0),
		       COALESCE(SUM(cache_creation_tokens), 0)
		FROM costs
		WHERE occurred_at >= ?`, since)
	var t Totals
	if err := row.Scan(&t.Episodes, &t.InputTokens, &t.OutputTokens, &t.CacheReadTokens, &t.CacheCreationTokens); err != nil {
		return Totals{}, fmt.Errorf("economy: total since: %w", err)
	}
	return t, nil
}
