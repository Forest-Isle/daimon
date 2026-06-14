-- proposals: the anticipation engine's queue (DAIMON_BLUEPRINT.md §4.9). A sleep
-- job scans commitments due soon and writes proposals here — concrete next
-- actions the user will likely need but has not yet asked for. Delivery (Telegram
-- inline accept/dismiss) and acceptance->episode firing are later increments;
-- this table is the durable queue they read from.
CREATE TABLE IF NOT EXISTS proposals (
    id                TEXT PRIMARY KEY,
    title             TEXT NOT NULL,
    body              TEXT NOT NULL DEFAULT '',
    action_plan       TEXT NOT NULL DEFAULT '',   -- episode goal fired if accepted
    urgency           INTEGER NOT NULL DEFAULT 0,  -- 0 low .. 3 urgent
    source_commitment TEXT NOT NULL DEFAULT '',
    state             TEXT NOT NULL DEFAULT 'pending', -- pending | accepted | dismissed | expired
    created_at        INTEGER NOT NULL,
    expires_at        INTEGER NOT NULL DEFAULT 0,
    decided_at        INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_proposals_pending ON proposals(state, expires_at);
