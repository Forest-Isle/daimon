package graph

import (
	"context"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

func setupDecayTest(t *testing.T) (*SQLiteGraph, *GraphDecayer, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := store.Open(tmpDir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	g := NewSQLiteGraph(db)
	decayer := NewGraphDecayer(g, DefaultDecayConfig())
	return g, decayer, func() { _ = db.Close() }
}

func TestDecayResult_Total(t *testing.T) {
	r := DecayResult{
		HistoricalEdgesRemoved: 5,
		StaleEdgesInvalidated:  3,
		OrphanNodesRemoved:     2,
	}
	if r.Total() != 10 {
		t.Errorf("expected total 10, got %d", r.Total())
	}

	empty := DecayResult{}
	if empty.Total() != 0 {
		t.Errorf("expected total 0, got %d", empty.Total())
	}
}

func TestDefaultDecayConfig(t *testing.T) {
	cfg := DefaultDecayConfig()
	if cfg.MaxEdgeAge != 90*24*time.Hour {
		t.Errorf("unexpected MaxEdgeAge: %v", cfg.MaxEdgeAge)
	}
	if cfg.StaleActiveAge != 30*24*time.Hour {
		t.Errorf("unexpected StaleActiveAge: %v", cfg.StaleActiveAge)
	}
	if cfg.OrphanNodeAge != 60*24*time.Hour {
		t.Errorf("unexpected OrphanNodeAge: %v", cfg.OrphanNodeAge)
	}
}

func TestGraphDecayer_DeleteOldHistorical(t *testing.T) {
	g, _, cleanup := setupDecayTest(t)
	defer cleanup()
	ctx := context.Background()

	// Create nodes and an edge
	aID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "A"})
	bID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "B"})
	_, err := g.UpsertEdge(ctx, Edge{SourceID: aID, TargetID: bID, Type: "knows"})
	if err != nil {
		t.Fatal(err)
	}

	// Now upsert same edge to create a historical version (old one gets valid_to set)
	time.Sleep(10 * time.Millisecond)
	_, err = g.UpsertEdge(ctx, Edge{SourceID: aID, TargetID: bID, Type: "knows"})
	if err != nil {
		t.Fatal(err)
	}

	// Use aggressive config: MaxEdgeAge = 0 means skip, use 1ns to catch everything
	decayer := NewGraphDecayer(g, DecayConfig{
		MaxEdgeAge:     1 * time.Nanosecond,
		StaleActiveAge: 0, // skip
		OrphanNodeAge:  0, // skip
	})

	result, err := decayer.RunDecay(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.HistoricalEdgesRemoved != 1 {
		t.Errorf("expected 1 historical edge removed, got %d", result.HistoricalEdgesRemoved)
	}

	// Active edge should still exist
	triples, err := g.Neighbors(ctx, aID, "knows")
	if err != nil {
		t.Fatal(err)
	}
	if len(triples) != 1 {
		t.Errorf("expected 1 active neighbor, got %d", len(triples))
	}
}

func TestGraphDecayer_InvalidateStale(t *testing.T) {
	g, _, cleanup := setupDecayTest(t)
	defer cleanup()
	ctx := context.Background()

	// Create nodes and an edge with no provenance
	aID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "A"})
	bID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "B"})
	_, err := g.UpsertEdge(ctx, Edge{SourceID: aID, TargetID: bID, Type: "knows"})
	if err != nil {
		t.Fatal(err)
	}

	// Edge has no provenance, and with aggressive StaleActiveAge it should be invalidated
	decayer := NewGraphDecayer(g, DecayConfig{
		MaxEdgeAge:     0,
		StaleActiveAge: 1 * time.Nanosecond, // anything older than 1ns
		OrphanNodeAge:  0,
	})

	time.Sleep(time.Millisecond)
	result, err := decayer.RunDecay(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.StaleEdgesInvalidated != 1 {
		t.Errorf("expected 1 stale edge invalidated, got %d", result.StaleEdgesInvalidated)
	}

	// Edge should no longer be active
	triples, err := g.Neighbors(ctx, aID, "knows")
	if err != nil {
		t.Fatal(err)
	}
	if len(triples) != 0 {
		t.Errorf("expected 0 active neighbors after invalidation, got %d", len(triples))
	}
}

func TestGraphDecayer_InvalidateSkipsWithProvenance(t *testing.T) {
	g, _, cleanup := setupDecayTest(t)
	defer cleanup()
	ctx := context.Background()

	aID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "A"})
	bID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "B"})
	edgeID, _ := g.UpsertEdge(ctx, Edge{SourceID: aID, TargetID: bID, Type: "knows"})

	// Add provenance — this edge should be protected
	if err := g.AddProvenance(ctx, edgeID, "memory.md", "source1"); err != nil {
		t.Fatal(err)
	}

	decayer := NewGraphDecayer(g, DecayConfig{
		MaxEdgeAge:     0,
		StaleActiveAge: 1 * time.Nanosecond,
		OrphanNodeAge:  0,
	})

	time.Sleep(time.Millisecond)
	result, err := decayer.RunDecay(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.StaleEdgesInvalidated != 0 {
		t.Errorf("expected 0 stale edges invalidated (has provenance), got %d", result.StaleEdgesInvalidated)
	}
}

func TestGraphDecayer_RemoveOrphan(t *testing.T) {
	g, _, cleanup := setupDecayTest(t)
	defer cleanup()
	ctx := context.Background()

	// Create a node with no edges
	_, err := g.UpsertNode(ctx, Node{Type: "person", Name: "Orphan"})
	if err != nil {
		t.Fatal(err)
	}

	// Also create a connected node (should survive)
	aID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "Connected"})
	bID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "Other"})
	if _, err := g.UpsertEdge(ctx, Edge{SourceID: aID, TargetID: bID, Type: "knows"}); err != nil {
		t.Fatal(err)
	}

	decayer := NewGraphDecayer(g, DecayConfig{
		MaxEdgeAge:     0,
		StaleActiveAge: 0,
		OrphanNodeAge:  1 * time.Nanosecond,
	})

	time.Sleep(time.Millisecond)
	result, err := decayer.RunDecay(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.OrphanNodesRemoved != 1 {
		t.Errorf("expected 1 orphan node removed, got %d", result.OrphanNodesRemoved)
	}

	// Connected nodes should still exist
	node, err := g.FindNode(ctx, "person", "Connected")
	if err != nil {
		t.Fatal(err)
	}
	if node == nil {
		t.Error("Connected node should still exist")
	}

	// Orphan should be gone
	_, err = g.FindNode(ctx, "person", "Orphan")
	if err == nil {
		t.Error("Orphan node should have been removed")
	}
}

func TestGraphDecayer_NoopWithZeroConfig(t *testing.T) {
	g, _, cleanup := setupDecayTest(t)
	defer cleanup()
	ctx := context.Background()

	aID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "A"})
	bID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "B"})
	if _, err := g.UpsertEdge(ctx, Edge{SourceID: aID, TargetID: bID, Type: "knows"}); err != nil {
		t.Fatal(err)
	}

	// Zero config means all steps are skipped
	decayer := NewGraphDecayer(g, DecayConfig{})
	result, err := decayer.RunDecay(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total() != 0 {
		t.Errorf("expected no changes with zero config, got %d", result.Total())
	}
}
