-- 024_drop_knowledge_tables.sql: Remove knowledge base and knowledge graph tables.
-- The internal/knowledge package (KB + graph) was removed; migrations
-- 004_knowledge_base.sql, 005_knowledge_graph.sql, and 011_temporal_graph.sql
-- have been deleted. This cleans up any existing knowledge data on upgrade.

-- Knowledge base (004): FTS5 triggers must be dropped before their tables.
DROP TRIGGER IF EXISTS kb_chunks_fts_insert;
DROP TRIGGER IF EXISTS kb_chunks_fts_delete;
DROP TRIGGER IF EXISTS kb_chunks_fts_update;
DROP TABLE IF EXISTS kb_chunks_fts;
DROP TABLE IF EXISTS kb_chunks;
DROP TABLE IF EXISTS kb_sources;

-- Knowledge graph (005 + 011 temporal recreation): provenance references edges.
DROP TABLE IF EXISTS kg_provenance;
DROP TABLE IF EXISTS kg_edges;
DROP TABLE IF EXISTS kg_edges_new;
DROP TABLE IF EXISTS kg_nodes;
