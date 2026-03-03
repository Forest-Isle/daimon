-- 006_agent_traces.sql
-- Agent execution traces for multi-agent collaboration observability

CREATE TABLE IF NOT EXISTS agent_traces (
    id TEXT PRIMARY KEY,
    parent_id TEXT,
    session_id TEXT NOT NULL,
    agent_name TEXT NOT NULL,
    input TEXT,
    output TEXT,
    error TEXT,
    started_at DATETIME NOT NULL,
    ended_at DATETIME,
    duration_ms INTEGER,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_agent_traces_session ON agent_traces(session_id);
CREATE INDEX IF NOT EXISTS idx_agent_traces_parent ON agent_traces(parent_id);
CREATE INDEX IF NOT EXISTS idx_agent_traces_started ON agent_traces(started_at DESC);
