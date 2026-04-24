package graph

import (
	"context"
	"encoding/json"
	"fmt"
)

// PaginatedResult holds a page of graph query results.
type PaginatedResult struct {
	Triples    []Triple
	TotalCount int
	Offset     int
	Limit      int
	HasMore    bool
}

// NeighborsPaginated returns neighbors of nodeID with offset/limit pagination.
// edgeType filters by edge type if non-empty.
func (g *SQLiteGraph) NeighborsPaginated(ctx context.Context, nodeID, edgeType string, offset, limit int) (*PaginatedResult, error) {
	if limit <= 0 {
		limit = 50
	}

	// Build base WHERE clause
	where := `e.source_id = ? AND e.valid_to IS NULL`
	args := []any{nodeID}
	if edgeType != "" {
		where += ` AND e.type = ?`
		args = append(args, edgeType)
	}

	// Count query
	countQuery := fmt.Sprintf(
		`SELECT COUNT(*) FROM kg_edges e WHERE %s`, where)
	var total int
	if err := g.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count neighbors: %w", err)
	}

	// Data query with LIMIT/OFFSET
	dataQuery := fmt.Sprintf(
		`SELECT e.id, e.source_id, e.target_id, e.type, e.weight,
		        n.id, n.type, n.name, n.properties
		   FROM kg_edges e
		   JOIN kg_nodes n ON n.id = e.target_id
		  WHERE %s
		  ORDER BY e.created_at DESC
		  LIMIT ? OFFSET ?`, where)
	dataArgs := append(args, limit, offset) //nolint:gocritic

	rows, err := g.db.QueryContext(ctx, dataQuery, dataArgs...)
	if err != nil {
		return nil, fmt.Errorf("query neighbors: %w", err)
	}
	defer func() { _ = rows.Close() }()

	triples, err := scanTriples(rows, nodeID)
	if err != nil {
		return nil, err
	}

	return &PaginatedResult{
		Triples:    triples,
		TotalCount: total,
		Offset:     offset,
		Limit:      limit,
		HasMore:    offset+len(triples) < total,
	}, nil
}

// TraversePaginated returns traversal results from nodeID with depth limit and pagination.
func (g *SQLiteGraph) TraversePaginated(ctx context.Context, nodeID string, maxDepth, offset, limit int) (*PaginatedResult, error) {
	if maxDepth <= 0 {
		maxDepth = 3
	}
	if limit <= 0 {
		limit = 50
	}

	// Use recursive CTE for traversal, then paginate the result
	cteQuery := fmt.Sprintf(`
		WITH RECURSIVE traverse(source_id, target_id, edge_type, weight, depth) AS (
			SELECT e.source_id, e.target_id, e.type, e.weight, 1
			  FROM kg_edges e
			 WHERE e.source_id = ? AND e.valid_to IS NULL
			UNION ALL
			SELECT e.source_id, e.target_id, e.type, e.weight, t.depth + 1
			  FROM kg_edges e
			  JOIN traverse t ON e.source_id = t.target_id
			 WHERE t.depth < %d AND e.valid_to IS NULL
		)
		SELECT DISTINCT t.source_id, t.target_id, t.edge_type, t.weight,
		       n.id, n.type, n.name, n.properties
		  FROM traverse t
		  JOIN kg_nodes n ON n.id = t.target_id
		 ORDER BY t.depth`, maxDepth)

	// Count total results
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM (%s)`, cteQuery)
	var total int
	if err := g.db.QueryRowContext(ctx, countQuery, nodeID).Scan(&total); err != nil {
		return nil, fmt.Errorf("count traversal: %w", err)
	}

	// Paginated query
	pagedQuery := fmt.Sprintf(`%s LIMIT ? OFFSET ?`, cteQuery)
	rows, err := g.db.QueryContext(ctx, pagedQuery, nodeID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query traversal: %w", err)
	}
	defer func() { _ = rows.Close() }()

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
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &PaginatedResult{
		Triples:    triples,
		TotalCount: total,
		Offset:     offset,
		Limit:      limit,
		HasMore:    offset+len(triples) < total,
	}, nil
}
