CREATE TABLE IF NOT EXISTS commitments (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,              -- project | promise | deadline | watch | routine
    title TEXT NOT NULL,
    body TEXT DEFAULT '',
    state TEXT NOT NULL DEFAULT 'active',  -- active | waiting | done | dropped
    due_at DATETIME,
    horizon TEXT DEFAULT '',
    source_episode TEXT DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_commitments_state ON commitments(state);
CREATE INDEX IF NOT EXISTS idx_commitments_due ON commitments(due_at);

CREATE TABLE IF NOT EXISTS journal (
    id TEXT PRIMARY KEY,
    episode_id TEXT DEFAULT '',
    kind TEXT NOT NULL,              -- outcome | decision | correction | fact
    summary TEXT NOT NULL,
    detail TEXT DEFAULT '',
    occurred_at DATETIME NOT NULL DEFAULT (datetime('now')),
    rollup_id TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_journal_episode ON journal(episode_id);
CREATE INDEX IF NOT EXISTS idx_journal_occurred ON journal(occurred_at);
