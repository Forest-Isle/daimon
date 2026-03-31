-- 011_temporal_graph.sql: Add temporal validity to knowledge graph edges
-- Recreate kg_edges with temporal columns and partial unique index
-- (SQLite cannot ALTER TABLE to drop constraints, so we recreate)

CREATE TABLE IF NOT EXISTS kg_edges_new (
    id          TEXT PRIMARY KEY,
    source_id   TEXT NOT NULL REFERENCES kg_nodes(id) ON DELETE CASCADE,
    target_id   TEXT NOT NULL REFERENCES kg_nodes(id) ON DELETE CASCADE,
    type        TEXT NOT NULL,
    weight      REAL DEFAULT 1.0,
    properties  TEXT DEFAULT '{}',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    valid_from  DATETIME,
    valid_to    DATETIME
);

-- Migrate existing data: valid_from = created_at, valid_to = NULL (currently valid)
INSERT INTO kg_edges_new (id, source_id, target_id, type, weight, properties, created_at, valid_from, valid_to)
    SELECT id, source_id, target_id, type, weight, properties, created_at, created_at, NULL
      FROM kg_edges;

DROP TABLE IF EXISTS kg_edges;
ALTER TABLE kg_edges_new RENAME TO kg_edges;

-- Recreate indexes
CREATE INDEX IF NOT EXISTS idx_kg_edges_source ON kg_edges(source_id);
CREATE INDEX IF NOT EXISTS idx_kg_edges_target ON kg_edges(target_id);
CREATE INDEX IF NOT EXISTS idx_kg_edges_type ON kg_edges(type);
CREATE INDEX IF NOT EXISTS idx_kg_edges_valid_to ON kg_edges(valid_to);

-- Only one active (valid_to IS NULL) edge per (source, target, type)
CREATE UNIQUE INDEX IF NOT EXISTS idx_kg_edges_active ON kg_edges(source_id, target_id, type) WHERE valid_to IS NULL;

-- Recreate provenance foreign keys (CASCADE was on the old table)
-- kg_provenance references kg_edges(id) — the data is preserved since IDs didn't change
