-- 008_access_log.sql: Access tracking for forgetting curve
CREATE TABLE IF NOT EXISTS fact_access_log (
    fact_id TEXT NOT NULL,
    accessed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    session_id TEXT
);

CREATE INDEX IF NOT EXISTS idx_access_log_fact ON fact_access_log(fact_id);
CREATE INDEX IF NOT EXISTS idx_access_log_time ON fact_access_log(accessed_at);

-- Aggregated stats (updated periodically)
CREATE TABLE IF NOT EXISTS fact_access_stats (
    fact_id TEXT PRIMARY KEY,
    access_count INTEGER DEFAULT 0,
    last_access DATETIME,
    first_access DATETIME
);
