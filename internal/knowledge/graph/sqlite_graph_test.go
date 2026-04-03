package graph

import (
	"context"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

func setupTestGraph(t *testing.T) (*SQLiteGraph, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := store.Open(tmpDir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	g := NewSQLiteGraph(db)
	return g, func() { db.Close() }
}

func TestUpsertEdgeVersioning(t *testing.T) {
	g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()

	// Create nodes
	aliceID, err := g.UpsertNode(ctx, Node{Type: "person", Name: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	compAID, err := g.UpsertNode(ctx, Node{Type: "org", Name: "CompanyA"})
	if err != nil {
		t.Fatal(err)
	}
	compBID, err := g.UpsertNode(ctx, Node{Type: "org", Name: "CompanyB"})
	if err != nil {
		t.Fatal(err)
	}

	// Create first edge: Alice --works_at--> CompanyA
	edge1ID, err := g.UpsertEdge(ctx, Edge{SourceID: aliceID, TargetID: compAID, Type: "works_at"})
	if err != nil {
		t.Fatal(err)
	}
	if edge1ID == "" {
		t.Fatal("edge1 should be created")
	}

	// Create second edge: Alice --works_at--> CompanyB
	// This is a different (source, target, type) tuple, so it does NOT invalidate the first edge.
	// Both edges can coexist since they have different targets.
	time.Sleep(10 * time.Millisecond) // ensure different timestamp
	edge2ID, err := g.UpsertEdge(ctx, Edge{SourceID: aliceID, TargetID: compBID, Type: "works_at"})
	if err != nil {
		t.Fatal(err)
	}
	if edge2ID == "" {
		t.Fatal("edge2 should be created")
	}

	// Current neighbors should show both CompanyA and CompanyB
	// because UpsertEdge only invalidates edges with same (source, target, type)
	triples, err := g.Neighbors(ctx, aliceID, "works_at")
	if err != nil {
		t.Fatal(err)
	}

	foundA, foundB := false, false
	for _, tr := range triples {
		if tr.Object.Name == "CompanyA" {
			foundA = true
		}
		if tr.Object.Name == "CompanyB" {
			foundB = true
		}
	}
	if !foundA {
		t.Error("CompanyA should be in current neighbors (different target = different edge)")
	}
	if !foundB {
		t.Error("CompanyB should be in current neighbors")
	}

	// Now test same (source, target, type) versioning: re-upsert Alice->CompanyA
	// This should invalidate the old edge and create a new one
	time.Sleep(10 * time.Millisecond)
	edge3ID, err := g.UpsertEdge(ctx, Edge{SourceID: aliceID, TargetID: compAID, Type: "works_at"})
	if err != nil {
		t.Fatal(err)
	}
	if edge3ID == edge1ID {
		t.Error("new edge should have a different ID from the invalidated one")
	}

	// Should still see CompanyA (via new edge) and CompanyB
	triples, err = g.Neighbors(ctx, aliceID, "works_at")
	if err != nil {
		t.Fatal(err)
	}
	if len(triples) != 2 {
		t.Errorf("expected 2 active neighbors, got %d", len(triples))
	}
}

func TestNeighborsAtPointInTime(t *testing.T) {
	g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()

	aliceID, err := g.UpsertNode(ctx, Node{Type: "person", Name: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	compAID, err := g.UpsertNode(ctx, Node{Type: "org", Name: "CompanyA"})
	if err != nil {
		t.Fatal(err)
	}

	// Create edge
	_, err = g.UpsertEdge(ctx, Edge{SourceID: aliceID, TargetID: compAID, Type: "works_at"})
	if err != nil {
		t.Fatal(err)
	}

	// Query at current time should return the edge
	now := time.Now()
	triples, err := g.NeighborsAt(ctx, aliceID, "", &now)
	if err != nil {
		t.Fatal(err)
	}
	if len(triples) == 0 {
		t.Error("should find edge at current time")
	}

	// Query at past time should return nothing (edge didn't exist then)
	past := time.Now().Add(-24 * time.Hour)
	triples, err = g.NeighborsAt(ctx, aliceID, "", &past)
	if err != nil {
		t.Fatal(err)
	}
	// valid_from was set to "now" during UpsertEdge, so 24h ago should not find it
	if len(triples) > 0 {
		t.Error("should not find edge at past time")
	}
}

func TestNeighborsAtWithNilReturnsActive(t *testing.T) {
	g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()

	aID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "A"})
	bID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "B"})

	if _, err := g.UpsertEdge(ctx, Edge{SourceID: aID, TargetID: bID, Type: "knows"}); err != nil {
		t.Fatal(err)
	}

	// nil asOf should return only active edges (same as Neighbors)
	triples, err := g.NeighborsAt(ctx, aID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(triples) != 1 {
		t.Errorf("expected 1 active neighbor, got %d", len(triples))
	}
}

func TestTraverseCurrentState(t *testing.T) {
	g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()

	aID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "A"})
	bID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "B"})
	cID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "C"})

	if _, err := g.UpsertEdge(ctx, Edge{SourceID: aID, TargetID: bID, Type: "knows"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.UpsertEdge(ctx, Edge{SourceID: bID, TargetID: cID, Type: "knows"}); err != nil {
		t.Fatal(err)
	}

	triples, err := g.Traverse(ctx, aID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(triples) < 2 {
		t.Errorf("expected at least 2 triples, got %d", len(triples))
	}

	// Verify we can reach C through B
	foundC := false
	for _, tr := range triples {
		if tr.Object.Name == "C" {
			foundC = true
		}
	}
	if !foundC {
		t.Error("should find C via 2-hop traversal from A")
	}
}

func TestTraverseDepthLimit(t *testing.T) {
	g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()

	aID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "A"})
	bID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "B"})
	cID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "C"})

	if _, err := g.UpsertEdge(ctx, Edge{SourceID: aID, TargetID: bID, Type: "knows"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.UpsertEdge(ctx, Edge{SourceID: bID, TargetID: cID, Type: "knows"}); err != nil {
		t.Fatal(err)
	}

	// Depth 1 should only reach B, not C
	triples, err := g.Traverse(ctx, aID, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range triples {
		if tr.Object.Name == "C" {
			t.Error("should not find C with depth 1")
		}
	}
}

func TestUpsertNodeIdempotent(t *testing.T) {
	g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()

	id1, err := g.UpsertNode(ctx, Node{Type: "person", Name: "Alice"})
	if err != nil {
		t.Fatal(err)
	}

	// Upserting same (type, name) should return same ID
	id2, err := g.UpsertNode(ctx, Node{Type: "person", Name: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Errorf("expected same ID on upsert, got %s and %s", id1, id2)
	}
}

func TestFindNodeAndFindByName(t *testing.T) {
	g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := g.UpsertNode(ctx, Node{Type: "person", Name: "Alice"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.UpsertNode(ctx, Node{Type: "person", Name: "Bob"}); err != nil {
		t.Fatal(err)
	}

	// FindNode exact match
	node, err := g.FindNode(ctx, "person", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if node.Name != "Alice" {
		t.Errorf("expected Alice, got %s", node.Name)
	}

	// FindByName fuzzy
	nodes, err := g.FindByName(ctx, "li") // matches "Alice"
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "Alice" {
		t.Errorf("expected [Alice], got %v", nodes)
	}
}
