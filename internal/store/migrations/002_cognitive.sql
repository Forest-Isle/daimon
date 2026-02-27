-- 002_cognitive.sql: audit table for cognitive cycle executions
CREATE TABLE IF NOT EXISTS cognitive_cycles (
    id          TEXT PRIMARY KEY DEFAULT (hex(randomblob(8))),
    session_id  TEXT NOT NULL,
    goal        TEXT NOT NULL,
    complexity  TEXT NOT NULL,
    plan_summary TEXT,
    replan_count INTEGER DEFAULT 0,
    success_count INTEGER DEFAULT 0,
    failure_count INTEGER DEFAULT 0,
    denied_count  INTEGER DEFAULT 0,
    overall_confidence REAL,
    succeeded   INTEGER DEFAULT 0,
    final_answer TEXT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);
