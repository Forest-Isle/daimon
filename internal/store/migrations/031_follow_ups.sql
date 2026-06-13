-- follow_ups: one-shot future re-entry triggers planted by an episode's Outcome.
-- A timer follow-up fires once at fire_at, emitting an internal.followup event
-- that the heart routes back into a fresh episode (long-task continuation by
-- recompose, not checkpoint deserialization).
CREATE TABLE IF NOT EXISTS follow_ups (
    id             TEXT PRIMARY KEY,
    source_episode TEXT NOT NULL DEFAULT '',
    kind           TEXT NOT NULL DEFAULT 'timer',
    goal           TEXT NOT NULL,
    trigger        TEXT NOT NULL DEFAULT '',
    fire_at        INTEGER NOT NULL,
    state          TEXT NOT NULL DEFAULT 'pending', -- pending | fired | cancelled
    created_at     INTEGER NOT NULL DEFAULT (strftime('%s','now'))
);

CREATE INDEX IF NOT EXISTS idx_follow_ups_due ON follow_ups(state, fire_at);
