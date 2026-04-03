package graph

import (
	"context"
	"testing"
)

func TestSyncOnDeleteWeakensEdges(t *testing.T) {
	g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()

	// Create nodes and edge
	aID, err := g.UpsertNode(ctx, Node{Type: "person", Name: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	bID, err := g.UpsertNode(ctx, Node{Type: "org", Name: "Company"})
	if err != nil {
		t.Fatal(err)
	}
	edgeID, err := g.UpsertEdge(ctx, Edge{SourceID: aID, TargetID: bID, Type: "works_at"})
	if err != nil {
		t.Fatal(err)
	}

	// Add provenance
	if err := g.AddProvenance(ctx, edgeID, "memory", "fact_123"); err != nil {
		t.Fatal(err)
	}

	// Verify provenance count
	count, err := g.CountProvenance(ctx, edgeID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 provenance, got %d", count)
	}

	// Sync delete
	gs := NewGraphSync(g, nil) // no extractor needed for delete
	if err := gs.SyncOnDelete(ctx, "fact_123"); err != nil {
		t.Fatal(err)
	}

	// Provenance should be removed
	count, err = g.CountProvenance(ctx, edgeID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 provenance after delete, got %d", count)
	}

	// Edge weight should be weakened to 0.1
	var weight float64
	if err := g.db.QueryRowContext(ctx, "SELECT weight FROM kg_edges WHERE id = ?", edgeID).Scan(&weight); err != nil {
		t.Fatal(err)
	}
	if weight > 0.11 {
		t.Errorf("expected weight ~0.1, got %f", weight)
	}
}

func TestSyncOnDeleteMultiProvenance(t *testing.T) {
	g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()

	aID, err := g.UpsertNode(ctx, Node{Type: "person", Name: "Bob"})
	if err != nil {
		t.Fatal(err)
	}
	bID, err := g.UpsertNode(ctx, Node{Type: "concept", Name: "Go"})
	if err != nil {
		t.Fatal(err)
	}
	edgeID, err := g.UpsertEdge(ctx, Edge{SourceID: aID, TargetID: bID, Type: "knows"})
	if err != nil {
		t.Fatal(err)
	}

	// Add two provenance entries
	if err := g.AddProvenance(ctx, edgeID, "memory", "fact_1"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddProvenance(ctx, edgeID, "memory", "fact_2"); err != nil {
		t.Fatal(err)
	}

	// Delete one
	gs := NewGraphSync(g, nil)
	if err := gs.SyncOnDelete(ctx, "fact_1"); err != nil {
		t.Fatal(err)
	}

	// Should still have 1 provenance
	count, err := g.CountProvenance(ctx, edgeID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 remaining provenance, got %d", count)
	}

	// Weight should NOT be 0.1 (still has support)
	var weight float64
	if err := g.db.QueryRowContext(ctx, "SELECT weight FROM kg_edges WHERE id = ?", edgeID).Scan(&weight); err != nil {
		t.Fatal(err)
	}
	if weight < 0.5 {
		t.Errorf("weight should not be reduced with remaining provenance, got %f", weight)
	}
}

func TestSyncOnUpdateMovesProvenance(t *testing.T) {
	g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()

	aID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "Charlie"})
	bID, _ := g.UpsertNode(ctx, Node{Type: "concept", Name: "Rust"})
	edgeID, _ := g.UpsertEdge(ctx, Edge{SourceID: aID, TargetID: bID, Type: "knows"})

	// Add provenance with old fact ID
	if err := g.AddProvenance(ctx, edgeID, "memory", "old_fact_1"); err != nil {
		t.Fatal(err)
	}

	// Sync update: old_fact_1 → new_fact_1
	gs := NewGraphSync(g, nil)
	if err := gs.SyncOnUpdate(ctx, "old_fact_1", "new_fact_1", "Charlie knows Rust"); err != nil {
		t.Fatal(err)
	}

	// Old provenance should be gone
	edges, err := g.FindEdgesByProvenance(ctx, "old_fact_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges for old provenance, got %d", len(edges))
	}

	// New provenance should exist
	edges, err = g.FindEdgesByProvenance(ctx, "new_fact_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge for new provenance, got %d", len(edges))
	}
}
