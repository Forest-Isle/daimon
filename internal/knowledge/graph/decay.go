package graph

import (
	"context"
	"log/slog"
	"time"
)

// DecayConfig configures graph decay behavior.
type DecayConfig struct {
	// MaxEdgeAge is the maximum age for inactive edges (valid_to IS NOT NULL).
	// Historical edges older than this are permanently deleted.
	MaxEdgeAge time.Duration

	// StaleActiveAge is the age after which active edges (valid_to IS NULL)
	// with no provenance references are considered stale and invalidated.
	StaleActiveAge time.Duration

	// OrphanNodeAge is the age after which nodes with zero active edges are removed.
	OrphanNodeAge time.Duration
}

// DefaultDecayConfig returns sensible defaults for graph decay.
func DefaultDecayConfig() DecayConfig {
	return DecayConfig{
		MaxEdgeAge:     90 * 24 * time.Hour, // 90 days for historical edges
		StaleActiveAge: 30 * 24 * time.Hour, // 30 days for stale active edges
		OrphanNodeAge:  60 * 24 * time.Hour, // 60 days for orphan nodes
	}
}

// GraphDecayer performs periodic cleanup of stale graph data.
type GraphDecayer struct {
	g   *SQLiteGraph
	cfg DecayConfig
}

// NewGraphDecayer creates a decayer for the given graph.
func NewGraphDecayer(g *SQLiteGraph, cfg DecayConfig) *GraphDecayer {
	return &GraphDecayer{g: g, cfg: cfg}
}

// DecayResult holds the counts from a decay cycle.
type DecayResult struct {
	HistoricalEdgesRemoved int
	StaleEdgesInvalidated  int
	OrphanNodesRemoved     int
}

// Total returns the total number of items affected.
func (r DecayResult) Total() int {
	return r.HistoricalEdgesRemoved + r.StaleEdgesInvalidated + r.OrphanNodesRemoved
}

// RunDecay performs a full decay cycle. Returns counts of removed items.
func (d *GraphDecayer) RunDecay(ctx context.Context) (DecayResult, error) {
	var result DecayResult
	now := time.Now()

	// Step 1: Delete old historical edges (valid_to is set and older than MaxEdgeAge)
	if d.cfg.MaxEdgeAge > 0 {
		cutoff := now.Add(-d.cfg.MaxEdgeAge)
		n, err := d.deleteOldHistoricalEdges(ctx, cutoff)
		if err != nil {
			return result, err
		}
		result.HistoricalEdgesRemoved = n
	}

	// Step 2: Invalidate stale active edges (valid_to IS NULL, no provenance, old)
	if d.cfg.StaleActiveAge > 0 {
		cutoff := now.Add(-d.cfg.StaleActiveAge)
		n, err := d.invalidateStaleEdges(ctx, cutoff, now)
		if err != nil {
			return result, err
		}
		result.StaleEdgesInvalidated = n
	}

	// Step 3: Remove orphan nodes (no active edges at all)
	if d.cfg.OrphanNodeAge > 0 {
		cutoff := now.Add(-d.cfg.OrphanNodeAge)
		n, err := d.removeOrphanNodes(ctx, cutoff)
		if err != nil {
			return result, err
		}
		result.OrphanNodesRemoved = n
	}

	if result.Total() > 0 {
		slog.Info("graph decay: cleanup complete",
			"historical_edges", result.HistoricalEdgesRemoved,
			"stale_edges", result.StaleEdgesInvalidated,
			"orphan_nodes", result.OrphanNodesRemoved,
		)
	}

	return result, nil
}

func (d *GraphDecayer) deleteOldHistoricalEdges(ctx context.Context, cutoff time.Time) (int, error) {
	res, err := d.g.db.ExecContext(ctx,
		`DELETE FROM kg_edges WHERE valid_to IS NOT NULL AND valid_to < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (d *GraphDecayer) invalidateStaleEdges(ctx context.Context, cutoff, now time.Time) (int, error) {
	res, err := d.g.db.ExecContext(ctx,
		`UPDATE kg_edges SET valid_to = ?
		 WHERE valid_to IS NULL
		 AND created_at < ?
		 AND id NOT IN (SELECT DISTINCT edge_id FROM kg_provenance)`,
		now, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (d *GraphDecayer) removeOrphanNodes(ctx context.Context, cutoff time.Time) (int, error) {
	res, err := d.g.db.ExecContext(ctx,
		`DELETE FROM kg_nodes WHERE created_at < ?
		 AND id NOT IN (SELECT source_id FROM kg_edges WHERE valid_to IS NULL)
		 AND id NOT IN (SELECT target_id FROM kg_edges WHERE valid_to IS NULL)`,
		cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
