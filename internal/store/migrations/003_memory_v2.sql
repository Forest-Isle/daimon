-- 003_memory_v2.sql: LEGACY - Extended memory facts table (replaced by file-based storage in 006)
-- This migration is kept for backward compatibility with existing databases
-- New installations should use file-based storage (see 006_file_memory_index.sql)

CREATE TABLE IF NOT EXISTS memory_facts (
    id           TEXT PRIMARY KEY,
    session_id   TEXT,
    user_id      TEXT,
    scope        TEXT NOT NULL DEFAULT 'session',
    content      TEXT NOT NULL,
    embedding    BLOB,
    category     TEXT,
    version      INTEGER DEFAULT 1,
    expires_at   DATETIME,
    metadata     TEXT DEFAULT '{}',
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_memory_facts_session ON memory_facts(session_id);
CREATE INDEX IF NOT EXISTS idx_memory_facts_user ON memory_facts(user_id);
CREATE INDEX IF NOT EXISTS idx_memory_facts_scope ON memory_facts(scope);
CREATE INDEX IF NOT EXISTS idx_memory_facts_expires ON memory_facts(expires_at) WHERE expires_at IS NOT NULL;

-- FTS5 virtual table for BM25 full-text search
-- Note: FTS5 may not be available in all SQLite builds; the application handles graceful degradation
CREATE VIRTUAL TABLE IF NOT EXISTS memory_facts_fts USING fts5(
    content,
    content='memory_facts',
    content_rowid='rowid',
    tokenize='porter unicode61'
);

-- Sync triggers to keep FTS index up to date
CREATE TRIGGER IF NOT EXISTS memory_facts_fts_insert
    AFTER INSERT ON memory_facts BEGIN
    INSERT INTO memory_facts_fts(rowid, content) VALUES (new.rowid, new.content);
END;

CREATE TRIGGER IF NOT EXISTS memory_facts_fts_delete
    AFTER DELETE ON memory_facts BEGIN
    INSERT INTO memory_facts_fts(memory_facts_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
END;

CREATE TRIGGER IF NOT EXISTS memory_facts_fts_update
    AFTER UPDATE ON memory_facts BEGIN
    INSERT INTO memory_facts_fts(memory_facts_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
    INSERT INTO memory_facts_fts(rowid, content) VALUES (new.rowid, new.content);
END;
