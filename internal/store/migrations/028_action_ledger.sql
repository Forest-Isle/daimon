CREATE TABLE IF NOT EXISTS trust_ledger (
    action_class TEXT NOT NULL,
    context_key  TEXT NOT NULL,
    attempts     INTEGER NOT NULL DEFAULT 0,
    verified_ok  INTEGER NOT NULL DEFAULT 0,
    corrected    INTEGER NOT NULL DEFAULT 0,
    level        INTEGER NOT NULL DEFAULT 0,
    updated_at   DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (action_class, context_key)
);

CREATE TABLE IF NOT EXISTS undo_journal (
    receipt_id TEXT PRIMARY KEY,
    tool_name  TEXT NOT NULL,
    undo_spec  TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    expires_at DATETIME,
    undone_at  DATETIME
);

CREATE TABLE IF NOT EXISTS holds (
    id         TEXT PRIMARY KEY,
    receipt_id TEXT NOT NULL,
    tool_name  TEXT NOT NULL,
    payload    TEXT NOT NULL DEFAULT '',
    execute_at DATETIME NOT NULL,
    state      TEXT NOT NULL DEFAULT 'pending',
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_holds_state ON holds(state);
CREATE INDEX IF NOT EXISTS idx_holds_execute_at ON holds(execute_at);
