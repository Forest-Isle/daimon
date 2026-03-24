-- Migration 006: File-based memory system with embeddings.db
-- This migration creates tables for the new file-based memory system.
-- The embeddings.db only stores embeddings and search indexes, not full content.

-- fact_embeddings: Stores embeddings and metadata for facts stored in markdown files
CREATE TABLE IF NOT EXISTS fact_embeddings (
    fact_id TEXT PRIMARY KEY,
    file_path TEXT NOT NULL,
    scope TEXT NOT NULL,
    user_id TEXT,
    session_id TEXT,
    content_hash TEXT NOT NULL,
    embedding BLOB NOT NULL,
    version INTEGER DEFAULT 1,
    expires_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_fact_embeddings_scope ON fact_embeddings(scope);
CREATE INDEX IF NOT EXISTS idx_fact_embeddings_user_id ON fact_embeddings(user_id);
CREATE INDEX IF NOT EXISTS idx_fact_embeddings_session_id ON fact_embeddings(session_id);
CREATE INDEX IF NOT EXISTS idx_fact_embeddings_hash ON fact_embeddings(content_hash);
CREATE INDEX IF NOT EXISTS idx_fact_embeddings_expires ON fact_embeddings(expires_at);

-- FTS5 virtual table for BM25 search
-- Note: content is stored externally in markdown files
CREATE VIRTUAL TABLE IF NOT EXISTS fact_embeddings_fts USING fts5(
    fact_id UNINDEXED,
    content,
    content='',
    tokenize='porter unicode61'
);

-- Triggers to keep FTS5 in sync
CREATE TRIGGER IF NOT EXISTS fact_embeddings_fts_insert AFTER INSERT ON fact_embeddings BEGIN
    INSERT INTO fact_embeddings_fts(rowid, fact_id, content)
    VALUES (NEW.rowid, NEW.fact_id, '');
END;

CREATE TRIGGER IF NOT EXISTS fact_embeddings_fts_delete AFTER DELETE ON fact_embeddings BEGIN
    DELETE FROM fact_embeddings_fts WHERE rowid = OLD.rowid;
END;

-- VSS virtual table for HNSW vector search (optional, created at runtime if enabled)
-- CREATE VIRTUAL TABLE IF NOT EXISTS fact_embeddings_vss USING vss0(embedding(1536));
