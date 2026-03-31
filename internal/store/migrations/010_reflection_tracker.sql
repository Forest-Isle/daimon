CREATE TABLE IF NOT EXISTS reflection_tracker_state (
    user_id TEXT PRIMARY KEY,
    unreflected_count INTEGER NOT NULL DEFAULT 0,
    unreflected_fact_ids TEXT NOT NULL DEFAULT '',
    running_topic_embedding BLOB,
    last_reflection_topic BLOB,
    l1_count_since_last_l2 INTEGER NOT NULL DEFAULT 0,
    l1_reflection_ids TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
