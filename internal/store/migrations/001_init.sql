CREATE TABLE IF NOT EXISTS sessions (
    id          TEXT PRIMARY KEY,
    channel     TEXT NOT NULL,
    channel_id  TEXT NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    metadata    TEXT DEFAULT '{}',
    UNIQUE(channel, channel_id)
);

CREATE TABLE IF NOT EXISTS messages (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES sessions(id),
    role        TEXT NOT NULL CHECK(role IN ('user', 'assistant', 'system', 'tool_use', 'tool_result')),
    content     TEXT NOT NULL,
    tool_name   TEXT,
    tool_input  TEXT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, created_at);

CREATE TABLE IF NOT EXISTS memories (
    id          TEXT PRIMARY KEY,
    session_id  TEXT,
    content     TEXT NOT NULL,
    embedding   BLOB,
    metadata    TEXT DEFAULT '{}',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS scheduled_tasks (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    cron_expr   TEXT NOT NULL,
    prompt      TEXT NOT NULL,
    channel     TEXT NOT NULL,
    channel_id  TEXT NOT NULL,
    enabled     INTEGER DEFAULT 1,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_run    DATETIME
);

CREATE TABLE IF NOT EXISTS tool_log (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES sessions(id),
    tool_name   TEXT NOT NULL,
    input       TEXT,
    output      TEXT,
    status      TEXT NOT NULL CHECK(status IN ('success', 'error', 'denied', 'timeout')),
    duration_ms INTEGER,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_tool_log_session ON tool_log(session_id, created_at);
