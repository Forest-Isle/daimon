-- costs: per-episode token-usage ledger (DAIMON_BLUEPRINT.md §4.11 economy). Each
-- episode records one row with the tokens it consumed across all of its provider
-- calls, so the agent can later account for what it spent and (with the C2b report)
-- answer "how much did it cost this month, and was it worth it". Token counts are
-- stored raw; the dollar cost is computed at report time from a model price table,
-- so price changes never require backfilling this table.
--
-- activity_class is the routing kind the episode was spawned under (chat, a heart
-- verdict kind, etc.) for ROI-by-activity-class reporting; it is populated by a
-- later increment (C2b) and is empty for now.
CREATE TABLE IF NOT EXISTS costs (
    id                    TEXT PRIMARY KEY,
    episode_id            TEXT NOT NULL DEFAULT '',
    model                 TEXT NOT NULL DEFAULT '',
    provider              TEXT NOT NULL DEFAULT '',
    activity_class        TEXT NOT NULL DEFAULT '',
    input_tokens          INTEGER NOT NULL DEFAULT 0,
    output_tokens         INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens     INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    occurred_at           INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_costs_occurred ON costs(occurred_at);
CREATE INDEX IF NOT EXISTS idx_costs_class ON costs(activity_class, occurred_at);
