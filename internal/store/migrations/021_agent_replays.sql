CREATE TABLE IF NOT EXISTS agent_replays (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    agent_mode TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'recording',
    model TEXT NOT NULL DEFAULT '',
    message_count INTEGER NOT NULL DEFAULT 0,
    started_at DATETIME NOT NULL DEFAULT (datetime('now')),
    completed_at DATETIME,
    metadata TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_agent_replays_session ON agent_replays(session_id);
CREATE INDEX IF NOT EXISTS idx_agent_replays_status ON agent_replays(status);
CREATE INDEX IF NOT EXISTS idx_agent_replays_started ON agent_replays(started_at);

CREATE TABLE IF NOT EXISTS agent_replay_events (
    id TEXT PRIMARY KEY,
    replay_id TEXT NOT NULL REFERENCES agent_replays(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    timestamp DATETIME NOT NULL DEFAULT (datetime('now')),
    duration_ms INTEGER,
    data TEXT NOT NULL DEFAULT '{}',
    UNIQUE(replay_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_replay_events_replay ON agent_replay_events(replay_id);
CREATE INDEX IF NOT EXISTS idx_replay_events_seq ON agent_replay_events(replay_id, sequence);
