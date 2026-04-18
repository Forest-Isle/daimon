CREATE TABLE IF NOT EXISTS task_ledger (
    id TEXT PRIMARY KEY,
    parent_id TEXT DEFAULT '',
    kind TEXT NOT NULL DEFAULT 'user_request',
    state TEXT NOT NULL DEFAULT 'pending',
    title TEXT NOT NULL DEFAULT '',
    description TEXT DEFAULT '',
    assignee TEXT DEFAULT '',
    depends_on TEXT DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at DATETIME,
    completed_at DATETIME,
    heartbeat DATETIME,
    result TEXT DEFAULT '',
    metadata TEXT DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_task_ledger_state ON task_ledger(state);
CREATE INDEX IF NOT EXISTS idx_task_ledger_parent ON task_ledger(parent_id);
CREATE INDEX IF NOT EXISTS idx_task_ledger_kind ON task_ledger(kind);
