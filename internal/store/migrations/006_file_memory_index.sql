-- Migration 006: File-based memory index
-- Creates auxiliary SQLite index tables for file-based memory storage

-- memory_index: Metadata index for fast filtering
CREATE TABLE IF NOT EXISTS memory_index (
    memory_id TEXT PRIMARY KEY,
    file_path TEXT NOT NULL,
    scope TEXT NOT NULL,
    user_id TEXT,
    session_id TEXT,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    strength REAL DEFAULT 1.0,
    UNIQUE(file_path)
);

CREATE INDEX IF NOT EXISTS idx_memory_index_scope ON memory_index(scope);
CREATE INDEX IF NOT EXISTS idx_memory_index_user_id ON memory_index(user_id);
CREATE INDEX IF NOT EXISTS idx_memory_index_session_id ON memory_index(session_id);
CREATE INDEX IF NOT EXISTS idx_memory_index_created_at ON memory_index(created_at);
CREATE INDEX IF NOT EXISTS idx_memory_index_strength ON memory_index(strength);

-- memory_fts: FTS5 full-text search index
CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
    memory_id UNINDEXED,
    content,
    tokenize = 'porter unicode61'
);

-- memory_embeddings: Vector embeddings for semantic search
CREATE TABLE IF NOT EXISTS memory_embeddings (
    memory_id TEXT PRIMARY KEY,
    embedding BLOB NOT NULL,
    dimension INTEGER NOT NULL,
    FOREIGN KEY (memory_id) REFERENCES memory_index(memory_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_memory_embeddings_dimension ON memory_embeddings(dimension);
