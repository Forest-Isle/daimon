-- 005_knowledge_graph.sql: Knowledge graph tables for entity/relation storage
CREATE TABLE IF NOT EXISTS kg_nodes (
    id          TEXT PRIMARY KEY,
    type        TEXT NOT NULL,  -- "person" | "org" | "concept" | "location" | "product" etc.
    name        TEXT NOT NULL,
    properties  TEXT DEFAULT '{}',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(type, name)
);

CREATE TABLE IF NOT EXISTS kg_edges (
    id          TEXT PRIMARY KEY,
    source_id   TEXT NOT NULL REFERENCES kg_nodes(id) ON DELETE CASCADE,
    target_id   TEXT NOT NULL REFERENCES kg_nodes(id) ON DELETE CASCADE,
    type        TEXT NOT NULL,  -- "knows" | "works_at" | "related_to" | "part_of" etc.
    weight      REAL DEFAULT 1.0,
    properties  TEXT DEFAULT '{}',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(source_id, target_id, type)
);

CREATE INDEX IF NOT EXISTS idx_kg_edges_source ON kg_edges(source_id);
CREATE INDEX IF NOT EXISTS idx_kg_edges_target ON kg_edges(target_id);
CREATE INDEX IF NOT EXISTS idx_kg_edges_type ON kg_edges(type);

-- Provenance: links edges back to their source (memory.md fact or KB chunk)
CREATE TABLE IF NOT EXISTS kg_provenance (
    edge_id     TEXT NOT NULL REFERENCES kg_edges(id) ON DELETE CASCADE,
    source_type TEXT NOT NULL,  -- "memory.md" | "kb_chunk"
    source_id   TEXT NOT NULL,
    PRIMARY KEY (edge_id, source_id)
);
