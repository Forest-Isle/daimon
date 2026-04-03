package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

// SQLiteGraph implements Graph using SQLite adjacency tables + recursive CTE.
type SQLiteGraph struct {
	db *store.DB
}

// NewSQLiteGraph creates a SQLiteGraph backed by the given DB.
func NewSQLiteGraph(db *store.DB) *SQLiteGraph {
	return &SQLiteGraph{db: db}
}

// UpsertNode creates or updates a node. Returns the node ID.
func (g *SQLiteGraph) UpsertNode(ctx context.Context, node Node) (string, error) {
	if node.Type == "" || node.Name == "" {
		return "", fmt.Errorf("node type and name are required")
	}

	props, _ := json.Marshal(node.Properties)
	now := time.Now()

	// Try to find existing
	var id string
	err := g.db.QueryRowContext(ctx,
		`SELECT id FROM kg_nodes WHERE type = ? AND name = ?`, node.Type, node.Name).Scan(&id)
	if err == nil {
		// Update
		_, err2 := g.db.ExecContext(ctx,
			`UPDATE kg_nodes SET properties = ?, updated_at = ? WHERE id = ?`,
			string(props), now, id)
		return id, err2
	}

	if node.ID == "" {
		node.ID = fmt.Sprintf("node_%d", now.UnixNano())
	}
	_, err = g.db.ExecContext(ctx,
		`INSERT INTO kg_nodes (id, type, name, properties, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		node.ID, node.Type, node.Name, string(props), now, now)
	if err != nil {
		return "", err
	}
	return node.ID, nil
}

// UpsertEdge creates or updates an edge with temporal versioning.
// If an active edge (valid_to IS NULL) exists for the same (source, target, type),
// it is invalidated and a new version is created.
func (g *SQLiteGraph) UpsertEdge(ctx context.Context, edge Edge) (string, error) {
	if edge.SourceID == "" || edge.TargetID == "" || edge.Type == "" {
		return "", fmt.Errorf("edge source_id, target_id, and type are required")
	}

	props, _ := json.Marshal(edge.Properties)
	now := time.Now()

	// Invalidate existing active edge
	_, err := g.db.ExecContext(ctx,
		`UPDATE kg_edges SET valid_to = ? WHERE source_id = ? AND target_id = ? AND type = ? AND valid_to IS NULL`,
		now, edge.SourceID, edge.TargetID, edge.Type)
	if err != nil {
		return "", fmt.Errorf("invalidate old edge: %w", err)
	}

	if edge.ID == "" {
		edge.ID = fmt.Sprintf("edge_%d", now.UnixNano())
	}
	weight := edge.Weight
	if weight == 0 {
		weight = 1.0
	}
	_, err = g.db.ExecContext(ctx,
		`INSERT INTO kg_edges (id, source_id, target_id, type, weight, properties, created_at, valid_from, valid_to) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		edge.ID, edge.SourceID, edge.TargetID, edge.Type, weight, string(props), now, now)
	if err != nil {
		return "", err
	}
	return edge.ID, nil
}

// Neighbors returns all nodes directly connected to nodeID via currently active edges.
func (g *SQLiteGraph) Neighbors(ctx context.Context, nodeID, edgeType string) ([]Triple, error) {
	return g.NeighborsAt(ctx, nodeID, edgeType, nil)
}

// NeighborsAt returns neighbors as of a specific point in time.
// If asOf is nil, only currently active edges (valid_to IS NULL) are returned.
func (g *SQLiteGraph) NeighborsAt(ctx context.Context, nodeID, edgeType string, asOf *time.Time) ([]Triple, error) {
	query := `
        SELECT e.id, e.source_id, e.target_id, e.type, e.weight,
               n.id, n.type, n.name, n.properties
          FROM kg_edges e
          JOIN kg_nodes n ON n.id = e.target_id
         WHERE e.source_id = ?`
	args := []any{nodeID}

	if asOf != nil {
		query += ` AND e.valid_from <= ? AND (e.valid_to IS NULL OR e.valid_to > ?)`
		args = append(args, *asOf, *asOf)
	} else {
		query += ` AND e.valid_to IS NULL`
	}

	if edgeType != "" {
		query += ` AND e.type = ?`
		args = append(args, edgeType)
	}

	rows, err := g.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanTriples(rows, nodeID)
}

// Traverse performs BFS multi-hop traversal from nodeID up to maxDepth using recursive CTE.
// Only currently active edges (valid_to IS NULL) are traversed.
func (g *SQLiteGraph) Traverse(ctx context.Context, nodeID string, maxDepth int) ([]Triple, error) {
	return g.TraverseAt(ctx, nodeID, maxDepth, nil)
}

// TraverseAt performs BFS multi-hop traversal as of a specific point in time.
// If asOf is nil, only currently active edges are traversed.
func (g *SQLiteGraph) TraverseAt(ctx context.Context, nodeID string, maxDepth int, asOf *time.Time) ([]Triple, error) {
	if maxDepth <= 0 {
		maxDepth = 3
	}

	var temporalFilter string
	var args []any

	if asOf != nil {
		temporalFilter = `AND e.valid_from <= ? AND (e.valid_to IS NULL OR e.valid_to > ?)`
		// Base case args: nodeID, asOf, asOf
		// Recursive case args: asOf, asOf (same filter)
		// We use named placeholders via positional args
	} else {
		temporalFilter = `AND e.valid_to IS NULL`
	}

	query := fmt.Sprintf(`
        WITH RECURSIVE traverse(source_id, target_id, edge_type, weight, depth) AS (
            SELECT e.source_id, e.target_id, e.type, e.weight, 1
              FROM kg_edges e
             WHERE e.source_id = ? %s
            UNION ALL
            SELECT e.source_id, e.target_id, e.type, e.weight, t.depth + 1
              FROM kg_edges e
              JOIN traverse t ON e.source_id = t.target_id
             WHERE t.depth < %d %s
        )
        SELECT DISTINCT t.source_id, t.target_id, t.edge_type, t.weight,
               n.id, n.type, n.name, n.properties
          FROM traverse t
          JOIN kg_nodes n ON n.id = t.target_id
         ORDER BY t.depth`, temporalFilter, maxDepth, temporalFilter)

	if asOf != nil {
		args = append(args, nodeID, *asOf, *asOf, *asOf, *asOf)
	} else {
		args = append(args, nodeID)
	}

	rows, err := g.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanTraverseTriples(rows)
}

// FindNode finds a node by exact type and name match.
func (g *SQLiteGraph) FindNode(ctx context.Context, nodeType, name string) (*Node, error) {
	var n Node
	var props string
	err := g.db.QueryRowContext(ctx,
		`SELECT id, type, name, properties, created_at, updated_at FROM kg_nodes WHERE type = ? AND name = ?`,
		nodeType, name).Scan(&n.ID, &n.Type, &n.Name, &props, &n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(props), &n.Properties) //nolint:errcheck
	return &n, nil
}

// FindByName fuzzy-matches nodes by name substring.
func (g *SQLiteGraph) FindByName(ctx context.Context, name string) ([]Node, error) {
	rows, err := g.db.QueryContext(ctx,
		`SELECT id, type, name, properties, created_at, updated_at FROM kg_nodes WHERE name LIKE ? ORDER BY name`,
		"%"+name+"%")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var nodes []Node
	for rows.Next() {
		var n Node
		var props string
		if err := rows.Scan(&n.ID, &n.Type, &n.Name, &props, &n.CreatedAt, &n.UpdatedAt); err != nil {
			continue
		}
		json.Unmarshal([]byte(props), &n.Properties) //nolint:errcheck
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// AddProvenance records the source of an edge.
func (g *SQLiteGraph) AddProvenance(ctx context.Context, edgeID, sourceType, sourceID string) error {
	_, err := g.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO kg_provenance (edge_id, source_type, source_id) VALUES (?, ?, ?)`,
		edgeID, sourceType, sourceID)
	return err
}

// FindEdgesByProvenance returns all active edges that have provenance from the given sourceID.
func (g *SQLiteGraph) FindEdgesByProvenance(ctx context.Context, sourceID string) ([]Edge, error) {
	rows, err := g.db.QueryContext(ctx,
		`SELECT e.id, e.source_id, e.target_id, e.type, e.weight, e.properties, e.created_at, e.valid_from, e.valid_to
		   FROM kg_edges e
		   JOIN kg_provenance p ON p.edge_id = e.id
		  WHERE p.source_id = ? AND e.valid_to IS NULL`, sourceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var edges []Edge
	for rows.Next() {
		var e Edge
		var props string
		var validFrom, validTo sql.NullTime
		if err := rows.Scan(&e.ID, &e.SourceID, &e.TargetID, &e.Type, &e.Weight, &props, &e.CreatedAt, &validFrom, &validTo); err != nil {
			continue
		}
		json.Unmarshal([]byte(props), &e.Properties) //nolint:errcheck
		if validFrom.Valid {
			e.ValidFrom = &validFrom.Time
		}
		if validTo.Valid {
			e.ValidTo = &validTo.Time
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// RemoveProvenance removes a specific provenance entry for an edge.
func (g *SQLiteGraph) RemoveProvenance(ctx context.Context, edgeID, sourceID string) error {
	_, err := g.db.ExecContext(ctx,
		`DELETE FROM kg_provenance WHERE edge_id = ? AND source_id = ?`,
		edgeID, sourceID)
	return err
}

// CountProvenance returns the number of provenance entries for an edge.
func (g *SQLiteGraph) CountProvenance(ctx context.Context, edgeID string) (int, error) {
	var count int
	err := g.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM kg_provenance WHERE edge_id = ?`, edgeID).Scan(&count)
	return count, err
}

// UpdateEdgeWeight updates the weight of an edge.
func (g *SQLiteGraph) UpdateEdgeWeight(ctx context.Context, edgeID string, weight float64) error {
	_, err := g.db.ExecContext(ctx,
		`UPDATE kg_edges SET weight = ? WHERE id = ?`, weight, edgeID)
	return err
}

// scanTriples scans rows from Neighbors query.
func scanTriples(rows *sql.Rows, _ string) ([]Triple, error) {
	var triples []Triple
	for rows.Next() {
		var edgeID, srcID, tgtID, edgeType string
		var weight float64
		var nID, nType, nName, nProps string
		if err := rows.Scan(&edgeID, &srcID, &tgtID, &edgeType, &weight,
			&nID, &nType, &nName, &nProps); err != nil {
			continue
		}
		var props map[string]string
		json.Unmarshal([]byte(nProps), &props) //nolint:errcheck
		triples = append(triples, Triple{
			Subject:   Node{ID: srcID},
			Predicate: edgeType,
			Object:    Node{ID: nID, Type: nType, Name: nName, Properties: props},
			Weight:    weight,
		})
	}
	return triples, rows.Err()
}

// scanTraverseTriples scans rows from the CTE Traverse query.
func scanTraverseTriples(rows *sql.Rows) ([]Triple, error) {
	var triples []Triple
	for rows.Next() {
		var srcID, tgtID, edgeType string
		var weight float64
		var nID, nType, nName, nProps string
		if err := rows.Scan(&srcID, &tgtID, &edgeType, &weight,
			&nID, &nType, &nName, &nProps); err != nil {
			continue
		}
		var props map[string]string
		json.Unmarshal([]byte(nProps), &props) //nolint:errcheck
		triples = append(triples, Triple{
			Subject:   Node{ID: srcID},
			Predicate: edgeType,
			Object:    Node{ID: nID, Type: nType, Name: nName, Properties: props},
			Weight:    weight,
		})
	}
	return triples, rows.Err()
}
