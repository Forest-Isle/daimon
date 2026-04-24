package graph

import (
	"context"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

func setupPaginationTest(t *testing.T) (*SQLiteGraph, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := store.Open(tmpDir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	g := NewSQLiteGraph(db)
	return g, func() { _ = db.Close() }
}

func TestNeighborsPaginated(t *testing.T) {
	g, cleanup := setupPaginationTest(t)
	defer cleanup()
	ctx := context.Background()

	// Create a hub node with 5 neighbors
	hubID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "Hub"})
	for _, name := range []string{"A", "B", "C", "D", "E"} {
		nID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: name})
		if _, err := g.UpsertEdge(ctx, Edge{SourceID: hubID, TargetID: nID, Type: "knows"}); err != nil {
			t.Fatal(err)
		}
	}

	// First page: 2 results
	page1, err := g.NeighborsPaginated(ctx, hubID, "knows", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if page1.TotalCount != 5 {
		t.Errorf("expected total 5, got %d", page1.TotalCount)
	}
	if len(page1.Triples) != 2 {
		t.Errorf("expected 2 triples, got %d", len(page1.Triples))
	}
	if !page1.HasMore {
		t.Error("expected HasMore=true")
	}

	// Second page
	page2, err := g.NeighborsPaginated(ctx, hubID, "knows", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Triples) != 2 {
		t.Errorf("expected 2 triples, got %d", len(page2.Triples))
	}
	if !page2.HasMore {
		t.Error("expected HasMore=true for second page")
	}

	// Last page
	page3, err := g.NeighborsPaginated(ctx, hubID, "knows", 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page3.Triples) != 1 {
		t.Errorf("expected 1 triple, got %d", len(page3.Triples))
	}
	if page3.HasMore {
		t.Error("expected HasMore=false for last page")
	}
}

func TestNeighborsPaginated_EdgeTypeFilter(t *testing.T) {
	g, cleanup := setupPaginationTest(t)
	defer cleanup()
	ctx := context.Background()

	hubID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "Hub"})
	aID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "A"})
	bID, _ := g.UpsertNode(ctx, Node{Type: "org", Name: "B"})

	g.UpsertEdge(ctx, Edge{SourceID: hubID, TargetID: aID, Type: "knows"})
	g.UpsertEdge(ctx, Edge{SourceID: hubID, TargetID: bID, Type: "works_at"})

	// Filter by "knows" only
	result, err := g.NeighborsPaginated(ctx, hubID, "knows", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 1 {
		t.Errorf("expected total 1 for 'knows', got %d", result.TotalCount)
	}

	// No filter
	result, err = g.NeighborsPaginated(ctx, hubID, "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 2 {
		t.Errorf("expected total 2 for all types, got %d", result.TotalCount)
	}
}

func TestNeighborsPaginated_DefaultLimit(t *testing.T) {
	g, cleanup := setupPaginationTest(t)
	defer cleanup()
	ctx := context.Background()

	hubID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "Hub"})

	// Limit <= 0 should default to 50
	result, err := g.NeighborsPaginated(ctx, hubID, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Limit != 50 {
		t.Errorf("expected default limit 50, got %d", result.Limit)
	}
}

func TestTraversePaginated(t *testing.T) {
	g, cleanup := setupPaginationTest(t)
	defer cleanup()
	ctx := context.Background()

	// Build chain: A -> B -> C -> D
	aID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "A"})
	bID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "B"})
	cID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "C"})
	dID, _ := g.UpsertNode(ctx, Node{Type: "person", Name: "D"})

	g.UpsertEdge(ctx, Edge{SourceID: aID, TargetID: bID, Type: "knows"})
	g.UpsertEdge(ctx, Edge{SourceID: bID, TargetID: cID, Type: "knows"})
	g.UpsertEdge(ctx, Edge{SourceID: cID, TargetID: dID, Type: "knows"})

	// Traverse 3 levels from A, page size 2
	page1, err := g.TraversePaginated(ctx, aID, 3, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if page1.TotalCount != 3 {
		t.Errorf("expected total 3, got %d", page1.TotalCount)
	}
	if len(page1.Triples) != 2 {
		t.Errorf("expected 2 triples, got %d", len(page1.Triples))
	}
	if !page1.HasMore {
		t.Error("expected HasMore=true")
	}

	// Second page
	page2, err := g.TraversePaginated(ctx, aID, 3, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Triples) != 1 {
		t.Errorf("expected 1 triple, got %d", len(page2.Triples))
	}
	if page2.HasMore {
		t.Error("expected HasMore=false")
	}
}

func TestPaginatedResult_Struct(t *testing.T) {
	r := PaginatedResult{
		Triples:    []Triple{{Predicate: "test"}},
		TotalCount: 10,
		Offset:     5,
		Limit:      3,
		HasMore:    true,
	}
	if r.TotalCount != 10 {
		t.Error("TotalCount mismatch")
	}
	if r.Offset != 5 {
		t.Error("Offset mismatch")
	}
	if r.Limit != 3 {
		t.Error("Limit mismatch")
	}
	if !r.HasMore {
		t.Error("HasMore should be true")
	}
	if len(r.Triples) != 1 {
		t.Error("Triples length mismatch")
	}
}
