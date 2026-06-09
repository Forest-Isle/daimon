-- 025_scheduled_tasks.sql: Add scheduled tasks table for SchedulerChannel
CREATE TABLE IF NOT EXISTS scheduled_tasks (
    id          TEXT PRIMARY KEY,
    prompt      TEXT NOT NULL,
    cron_expr   TEXT NOT NULL,
    notify_to   TEXT NOT NULL DEFAULT 'telegram',
    notify_id   TEXT NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    last_run_at TEXT,
    last_status TEXT
);
