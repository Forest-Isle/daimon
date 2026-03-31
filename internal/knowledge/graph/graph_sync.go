package graph

import (
	"context"
	"log/slog"
)

// GraphSync synchronizes memory lifecycle events with the knowledge graph.
type GraphSync struct {
	graph     *SQLiteGraph
	extractor *LLMEntityExtractor
}

// NewGraphSync creates a new GraphSync that bridges memory events to graph updates.
func NewGraphSync(graph *SQLiteGraph, extractor *LLMEntityExtractor) *GraphSync {
	return &GraphSync{graph: graph, extractor: extractor}
}

// SyncOnAdd extracts entities from new memory content and adds them to the graph.
func (gs *GraphSync) SyncOnAdd(ctx context.Context, factID, content string) error {
	if gs.extractor != nil {
		return gs.extractor.Extract(ctx, content, "memory", factID)
	}
	return nil
}

// SyncOnUpdate handles a memory update by updating provenance references
// and extracting entities from the new content.
func (gs *GraphSync) SyncOnUpdate(ctx context.Context, oldFactID, newFactID, content string) error {
	gs.updateProvenance(ctx, oldFactID, newFactID)
	return gs.SyncOnAdd(ctx, newFactID, content)
}

// SyncOnDelete removes provenance for a deleted memory and reduces edge weights
// for edges that lose all provenance support.
func (gs *GraphSync) SyncOnDelete(ctx context.Context, factID string) error {
	edges, err := gs.graph.FindEdgesByProvenance(ctx, factID)
	if err != nil {
		slog.Warn("graph_sync: find edges by provenance failed", "fact_id", factID, "err", err)
		return err
	}

	for _, edge := range edges {
		if err := gs.graph.RemoveProvenance(ctx, edge.ID, factID); err != nil {
			slog.Warn("graph_sync: remove provenance failed", "edge_id", edge.ID, "err", err)
			continue
		}

		count, err := gs.graph.CountProvenance(ctx, edge.ID)
		if err != nil {
			slog.Warn("graph_sync: count provenance failed", "edge_id", edge.ID, "err", err)
			continue
		}

		if count == 0 {
			// No remaining provenance — set weight to minimum
			if err := gs.graph.UpdateEdgeWeight(ctx, edge.ID, 0.1); err != nil {
				slog.Warn("graph_sync: update edge weight failed", "edge_id", edge.ID, "err", err)
			}
		}
	}
	return nil
}

// updateProvenance replaces old provenance source IDs with new ones.
func (gs *GraphSync) updateProvenance(ctx context.Context, oldSourceID, newSourceID string) {
	edges, err := gs.graph.FindEdgesByProvenance(ctx, oldSourceID)
	if err != nil {
		slog.Warn("graph_sync: find edges for provenance update failed", "old_source", oldSourceID, "err", err)
		return
	}

	for _, edge := range edges {
		if err := gs.graph.RemoveProvenance(ctx, edge.ID, oldSourceID); err != nil {
			slog.Warn("graph_sync: remove old provenance failed", "edge_id", edge.ID, "err", err)
			continue
		}
		if err := gs.graph.AddProvenance(ctx, edge.ID, "memory", newSourceID); err != nil {
			slog.Warn("graph_sync: add new provenance failed", "edge_id", edge.ID, "err", err)
		}
	}
}
