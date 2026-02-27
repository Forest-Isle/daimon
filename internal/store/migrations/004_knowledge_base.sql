-- 004_knowledge_base.sql: Knowledge base tables for document ingestion and retrieval
CREATE TABLE IF NOT EXISTS kb_sources (
    id           TEXT PRIMARY KEY,
    uri          TEXT NOT NULL,
    source_type  TEXT NOT NULL,  -- "markdown" | "pdf" | "code" | "web" | "text"
    title        TEXT,
    chunk_count  INTEGER DEFAULT 0,
    metadata     TEXT DEFAULT '{}',
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(uri)
);

CREATE TABLE IF NOT EXISTS kb_chunks (
    id           TEXT PRIMARY KEY,
    source_id    TEXT NOT NULL REFERENCES kb_sources(id) ON DELETE CASCADE,
    source_uri   TEXT NOT NULL,
    source_type  TEXT NOT NULL,
    content      TEXT NOT NULL,
    embedding    BLOB,
    chunk_index  INTEGER NOT NULL DEFAULT 0,
    metadata     TEXT DEFAULT '{}',
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_kb_chunks_source ON kb_chunks(source_id);
CREATE INDEX IF NOT EXISTS idx_kb_chunks_type ON kb_chunks(source_type);

-- FTS5 for BM25 search over knowledge base chunks
CREATE VIRTUAL TABLE IF NOT EXISTS kb_chunks_fts USING fts5(
    content,
    content='kb_chunks',
    content_rowid='rowid',
    tokenize='porter unicode61'
);

CREATE TRIGGER IF NOT EXISTS kb_chunks_fts_insert
    AFTER INSERT ON kb_chunks BEGIN
    INSERT INTO kb_chunks_fts(rowid, content) VALUES (new.rowid, new.content);
END;

CREATE TRIGGER IF NOT EXISTS kb_chunks_fts_delete
    AFTER DELETE ON kb_chunks BEGIN
    INSERT INTO kb_chunks_fts(kb_chunks_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
END;

CREATE TRIGGER IF NOT EXISTS kb_chunks_fts_update
    AFTER UPDATE ON kb_chunks BEGIN
    INSERT INTO kb_chunks_fts(kb_chunks_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
    INSERT INTO kb_chunks_fts(rowid, content) VALUES (new.rowid, new.content);
END;
