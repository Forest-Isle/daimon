CREATE TABLE IF NOT EXISTS attention_feedback (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id        TEXT NOT NULL DEFAULT '',
    expected_action TEXT NOT NULL,
    given_action    TEXT NOT NULL,
    note            TEXT NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_attention_feedback_created ON attention_feedback(created_at);
