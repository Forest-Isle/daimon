CREATE TABLE IF NOT EXISTS memory_audit_log (
    id TEXT PRIMARY KEY,
    memory_id TEXT NOT NULL,
    action TEXT NOT NULL,
    actor TEXT NOT NULL DEFAULT 'system',
    timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    details TEXT
);

CREATE INDEX IF NOT EXISTS idx_memory_audit_log_memory_id ON memory_audit_log(memory_id);
CREATE INDEX IF NOT EXISTS idx_memory_audit_log_timestamp ON memory_audit_log(timestamp);
