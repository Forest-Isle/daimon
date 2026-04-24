# Knowledge Graph Optimization — Decay, Pagination & Cache

## Overview

Addresses the knowledge graph's unbounded growth problem by adding three mechanisms: temporal edge decay with automatic cleanup, paginated traversal queries, and an in-memory connectivity cache for frequently accessed patterns.

## Problem

The SQLite-backed knowledge graph had no cleanup mechanisms:
- Historical edges (superseded versions with `valid_to` set) accumulated forever
- Active edges with no provenance could never be invalidated
- Orphan nodes (disconnected from all edges) were never removed
- Large traversals (depth > 3) could become slow with no result limits
- Repeated neighbor queries re-executed identical SQL

## Components

### Edge Decay (`internal/knowledge/graph/decay.go`)

`GraphDecayer` performs 3-step cleanup:

```
Step 1: Delete old historical edges
        WHERE valid_to IS NOT NULL AND valid_to < cutoff(90d)

Step 2: Invalidate stale active edges  
        WHERE valid_to IS NULL AND created_at < cutoff(30d)
        AND id NOT IN (SELECT edge_id FROM kg_provenance)

Step 3: Remove orphan nodes
        WHERE created_at < cutoff(60d)
        AND id NOT IN active edges (source or target)
```

**Provenance protection**: Step 2 skips edges that have provenance records, ensuring edges with known sources are never automatically invalidated.

**Configuration**:
```go
DecayConfig{
    MaxEdgeAge:     90 * 24 * time.Hour,  // historical edges
    StaleActiveAge: 30 * 24 * time.Hour,  // unprovenienced active edges
    OrphanNodeAge:  60 * 24 * time.Hour,  // disconnected nodes
}
```

Zero duration on any field disables that cleanup step.

### Paginated Queries (`internal/knowledge/graph/pagination.go`)

Two paginated query methods:

```go
// Paginated neighbor lookup
result, err := graph.NeighborsPaginated(ctx, nodeID, edgeType, offset, limit)
// result.Triples, result.TotalCount, result.HasMore

// Paginated multi-hop traversal
result, err := graph.TraversePaginated(ctx, nodeID, maxDepth, offset, limit)
```

`PaginatedResult`:
```go
type PaginatedResult struct {
    Triples    []Triple
    TotalCount int     // total matching results
    Offset     int     // current offset
    Limit      int     // page size
    HasMore    bool    // more results available
}
```

Uses SQL `LIMIT/OFFSET` on the final result set. Default limit is 100 if not specified.

### Connectivity Cache (`internal/knowledge/graph/cache.go`)

In-memory TTL cache for frequently accessed graph queries:

```go
cache := NewConnectivityCache(1000, 5*time.Minute)

// Check cache first
if triples, ok := cache.Get("neighbors:node123:relation"); ok {
    return triples
}

// Query graph, cache result
triples := graph.Neighbors(ctx, "node123", "relation")
cache.Put("neighbors:node123:relation", triples)
```

**Eviction**: When cache reaches `maxSize`, the oldest entry (by creation time) is evicted. Simple and predictable.

**TTL**: Entries older than `ttl` are considered expired on read. No background cleanup goroutine needed.

**Invalidation**: `cache.Invalidate()` clears all entries (call after graph mutations).

## Files

| File | Lines | Description |
|---|---|---|
| `internal/knowledge/graph/decay.go` | 136 | 3-step decay with provenance protection |
| `internal/knowledge/graph/pagination.go` | 143 | Paginated Neighbors + Traverse |
| `internal/knowledge/graph/cache.go` | 80 | TTL connectivity cache with eviction |
| `internal/knowledge/graph/decay_test.go` | 235 | 7 tests with in-memory SQLite |
| `internal/knowledge/graph/pagination_test.go` | 189 | 6 tests for pagination + filtering |
| `internal/knowledge/graph/cache_test.go` | 128 | 6 tests for TTL, eviction, invalidation |

## Testing

```bash
go test ./internal/knowledge/graph/...
```

## Integration

Run decay periodically (e.g., in the evolution insights cycle):

```go
decayer := graph.NewGraphDecayer(sqliteGraph, graph.DefaultDecayConfig())

// In the 6-hour insights loop:
result, err := decayer.RunDecay(ctx)
slog.Info("graph decay", 
    "historical_removed", result.HistoricalEdgesRemoved,
    "stale_invalidated", result.StaleEdgesInvalidated,
    "orphans_removed", result.OrphanNodesRemoved)
```
