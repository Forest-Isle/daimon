package graph

import (
	"context"
	"log/slog"
	"time"
)

// GraphDecayTask periodically decays unsupported knowledge graph edges
// and cleans up orphaned provenance entries.
type GraphDecayTask struct {
	graph    *SQLiteGraph
	interval time.Duration
	done     chan struct{}
}

// NewGraphDecayTask creates a new decay task with the given interval.
// The default recommended interval is 24 hours.
func NewGraphDecayTask(graph *SQLiteGraph, interval time.Duration) *GraphDecayTask {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	return &GraphDecayTask{
		graph:    graph,
		interval: interval,
		done:     make(chan struct{}),
	}
}

// Start begins the background decay loop. It blocks until ctx is cancelled
// or Stop is called.
func (gd *GraphDecayTask) Start(ctx context.Context) {
	ticker := time.NewTicker(gd.interval)
	defer ticker.Stop()

	slog.Info("graph_decay: started", "interval", gd.interval)
	for {
		select {
		case <-ctx.Done():
			slog.Info("graph_decay: stopped via context")
			return
		case <-gd.done:
			slog.Info("graph_decay: stopped")
			return
		case <-ticker.C:
			if err := gd.Decay(ctx); err != nil {
				slog.Error("graph_decay: decay cycle failed", "err", err)
			}
		}
	}
}

// Stop signals the background loop to exit.
func (gd *GraphDecayTask) Stop() {
	close(gd.done)
}

// Decay performs a single decay cycle:
//  1. Remove orphaned provenance entries (pointing to non-existent memory sources).
//  2. Decay weights of edges with zero provenance support.
//  3. Delete dead edges (low weight and already invalidated).
func (gd *GraphDecayTask) Decay(ctx context.Context) error {
	// Step 1: Remove orphaned memory provenance
	orphanResult, err := gd.graph.db.ExecContext(ctx,
		`DELETE FROM kg_provenance
		 WHERE source_type = 'memory'
		   AND source_id NOT IN (SELECT memory_id FROM memory_index)`)
	if err != nil {
		slog.Warn("graph_decay: orphan cleanup failed", "err", err)
		// Continue with other steps even if this fails (memory_index may not exist)
	} else {
		if n, _ := orphanResult.RowsAffected(); n > 0 {
			slog.Info("graph_decay: removed orphaned provenance", "count", n)
		}
	}

	// Step 2: Decay unsupported edges (multiply weight by 0.9)
	decayResult, err := gd.graph.db.ExecContext(ctx,
		`UPDATE kg_edges SET weight = weight * 0.9
		 WHERE id NOT IN (SELECT DISTINCT edge_id FROM kg_provenance)`)
	if err != nil {
		return err
	}
	if n, _ := decayResult.RowsAffected(); n > 0 {
		slog.Info("graph_decay: decayed unsupported edges", "count", n)
	}

	// Step 3: Remove dead edges (low weight and already invalidated)
	deadResult, err := gd.graph.db.ExecContext(ctx,
		`DELETE FROM kg_edges WHERE weight < 0.1 AND valid_to IS NOT NULL`)
	if err != nil {
		return err
	}
	if n, _ := deadResult.RowsAffected(); n > 0 {
		slog.Info("graph_decay: removed dead edges", "count", n)
	}

	// Step 4: Clean up provenance for deleted edges
	_, err = gd.graph.db.ExecContext(ctx,
		`DELETE FROM kg_provenance WHERE edge_id NOT IN (SELECT id FROM kg_edges)`)
	if err != nil {
		return err
	}

	return nil
}
