CREATE TABLE IF NOT EXISTS execution_events (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    node_type TEXT NOT NULL,
    transitioned_to TEXT,
    execution_path TEXT NOT NULL,
    input_snapshot TEXT,
    output_snapshot TEXT,
    metadata TEXT,
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_execution_events_session ON execution_events(session_id);
CREATE INDEX IF NOT EXISTS idx_execution_events_created ON execution_events(created_at);
