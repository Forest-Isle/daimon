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
	"strings"

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

// ModelTotals is one model's aggregate, for the by-model cost report.
type ModelTotals struct {
	Model string
	Totals
}

// ClassModelTotals is one (activity class, model) aggregate, for the by-class
// cost report (blueprint §4.11). The CLI folds these per class so a class that
// spans models is priced with each model's own rate. An empty Class is the
// unclassified bucket (rows recorded before activity-class threading).
type ClassModelTotals struct {
	Class string
	Model string
	Totals
}

// Price is per-million-token USD rates for a model. Rates are supplied by the
// operator (config) — none are hard-coded, since they vary by provider and
// endpoint and change over time; an unpriced model is reported in tokens only.
type Price struct {
	InputPerMTok         float64
	OutputPerMTok        float64
	CacheReadPerMTok     float64
	CacheCreationPerMTok float64
}

// Prices maps a model id to its rates. Lookup is exact first, then a longest
// substring match, so "claude-opus-4-8" can be priced by a "claude-opus" key.
type Prices map[string]Price

// CostUSD computes the dollar cost of t under the model's price, returning
// priced=false when no rate is configured for the model (caller shows tokens only,
// never a misleading $0).
func (p Prices) CostUSD(model string, t Totals) (usd float64, priced bool) {
	pr, ok := p.lookup(model)
	if !ok {
		return 0, false
	}
	usd = (float64(t.InputTokens)*pr.InputPerMTok +
		float64(t.OutputTokens)*pr.OutputPerMTok +
		float64(t.CacheReadTokens)*pr.CacheReadPerMTok +
		float64(t.CacheCreationTokens)*pr.CacheCreationPerMTok) / 1e6
	return usd, true
}

func (p Prices) lookup(model string) (Price, bool) {
	// An empty model id is never priced — otherwise an empty config key would
	// silently price the "(unknown)" rows the report shows for blank models.
	if model == "" {
		return Price{}, false
	}
	if pr, ok := p[model]; ok {
		return pr, true
	}
	bestLen := 0
	bestKey := ""
	var best Price
	found := false
	for key, pr := range p {
		if key == "" || !strings.Contains(model, key) {
			continue
		}
		// Longest containing key wins; on a length tie the lexicographically
		// smaller key wins so the result never depends on Go's random map order.
		if len(key) > bestLen || (len(key) == bestLen && key < bestKey) {
			best, bestLen, bestKey, found = pr, len(key), key, true
		}
	}
	return best, found
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
		WHERE (? <= 0 OR occurred_at >= ?)`, since, since)
	var t Totals
	if err := row.Scan(&t.Episodes, &t.InputTokens, &t.OutputTokens, &t.CacheReadTokens, &t.CacheCreationTokens); err != nil {
		return Totals{}, fmt.Errorf("economy: total since: %w", err)
	}
	return t, nil
}

// ByModelSince aggregates cost rows per model at or after the epoch-second cutoff
// (since <= 0 ⇒ all rows), ordered by output tokens descending (the heaviest
// spenders first). It is the basis of the `daimon costs` report.
func (s *Store) ByModelSince(ctx context.Context, since int64) ([]ModelTotals, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT model,
		       COUNT(1),
		       COALESCE(SUM(input_tokens), 0),
		       COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(cache_read_tokens), 0),
		       COALESCE(SUM(cache_creation_tokens), 0)
		FROM costs
		WHERE (? <= 0 OR occurred_at >= ?)
		GROUP BY model
		ORDER BY SUM(output_tokens) DESC, model ASC`, since, since)
	if err != nil {
		return nil, fmt.Errorf("economy: by model: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ModelTotals
	for rows.Next() {
		var m ModelTotals
		if err := rows.Scan(&m.Model, &m.Episodes, &m.InputTokens, &m.OutputTokens, &m.CacheReadTokens, &m.CacheCreationTokens); err != nil {
			return nil, fmt.Errorf("economy: by model scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("economy: by model rows: %w", err)
	}
	return out, nil
}

// EpisodeClassCost is one episode's class and token cost, for joining cost to the
// episode's outcome quality (the ROI-by-class report, blueprint §4.11). Episodes
// (Totals.Episodes) is always 1 — the field is reused only so callers can sum
// rows into a per-class Totals with the same accumulation code as the other reports.
type EpisodeClassCost struct {
	EpisodeID string
	Class     string
	Totals
}

// EpisodeClassCostSince returns one row per episode (the cost ledger is idempotent
// per episode) at or after the epoch-second cutoff (since <= 0 ⇒ all rows),
// carrying each episode's activity class and tokens. It is the cost side of the
// ROI-by-class join; the value side (outcome quality) is looked up by episode id
// from the world journal. Ordered by episode id for deterministic output.
func (s *Store) EpisodeClassCostSince(ctx context.Context, since int64) ([]EpisodeClassCost, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT episode_id,
		       activity_class,
		       input_tokens,
		       output_tokens,
		       cache_read_tokens,
		       cache_creation_tokens
		FROM costs
		WHERE (? <= 0 OR occurred_at >= ?)
		ORDER BY episode_id ASC`, since, since)
	if err != nil {
		return nil, fmt.Errorf("economy: episode class cost: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []EpisodeClassCost
	for rows.Next() {
		var e EpisodeClassCost
		e.Episodes = 1
		var in, outp, cr, cc int64
		if err := rows.Scan(&e.EpisodeID, &e.Class, &in, &outp, &cr, &cc); err != nil {
			return nil, fmt.Errorf("economy: episode class cost scan: %w", err)
		}
		e.InputTokens, e.OutputTokens, e.CacheReadTokens, e.CacheCreationTokens = in, outp, cr, cc
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("economy: episode class cost rows: %w", err)
	}
	return out, nil
}

// ByClassModelSince aggregates cost rows per (activity class, model) at or after
// the epoch-second cutoff (since <= 0 ⇒ all rows). The CLI folds these per class
// — pricing each model sub-row at its own rate — to report cost by kind of work
// (chat vs each autonomous trigger). Ordered by class then model for determinism;
// the final per-class ordering is computed by the caller after folding.
func (s *Store) ByClassModelSince(ctx context.Context, since int64) ([]ClassModelTotals, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT activity_class,
		       model,
		       COUNT(1),
		       COALESCE(SUM(input_tokens), 0),
		       COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(cache_read_tokens), 0),
		       COALESCE(SUM(cache_creation_tokens), 0)
		FROM costs
		WHERE (? <= 0 OR occurred_at >= ?)
		GROUP BY activity_class, model
		ORDER BY activity_class ASC, model ASC`, since, since)
	if err != nil {
		return nil, fmt.Errorf("economy: by class+model: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ClassModelTotals
	for rows.Next() {
		var c ClassModelTotals
		if err := rows.Scan(&c.Class, &c.Model, &c.Episodes, &c.InputTokens, &c.OutputTokens, &c.CacheReadTokens, &c.CacheCreationTokens); err != nil {
			return nil, fmt.Errorf("economy: by class+model scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("economy: by class+model rows: %w", err)
	}
	return out, nil
}
