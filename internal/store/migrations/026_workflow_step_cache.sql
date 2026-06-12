CREATE TABLE IF NOT EXISTS workflow_step_cache (
    cache_key TEXT PRIMARY KEY,
    workflow_name TEXT NOT NULL,
    workflow_hash TEXT NOT NULL,
    stage_id TEXT NOT NULL,
    step_id TEXT NOT NULL,
    result_json TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_workflow_step_cache_workflow ON workflow_step_cache(workflow_name, workflow_hash);
CREATE INDEX IF NOT EXISTS idx_workflow_step_cache_step ON workflow_step_cache(step_id);
