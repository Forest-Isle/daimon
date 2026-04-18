CREATE TABLE IF NOT EXISTS task_checkpoints (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    subtask_index INTEGER NOT NULL DEFAULT 0,
    observations_json TEXT NOT NULL DEFAULT '[]',
    plan_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(session_id)
);

CREATE INDEX IF NOT EXISTS idx_task_checkpoints_session ON task_checkpoints(session_id);
