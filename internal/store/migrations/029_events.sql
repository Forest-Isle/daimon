CREATE TABLE IF NOT EXISTS events (
    id          TEXT PRIMARY KEY,
    source      TEXT NOT NULL,
    kind        TEXT NOT NULL,
    payload     TEXT NOT NULL DEFAULT '',
    occurred_at DATETIME NOT NULL DEFAULT (datetime('now')),
    dedup_key   TEXT NOT NULL DEFAULT '',
    routed_at   DATETIME,
    verdict     TEXT NOT NULL DEFAULT ''
);

-- Source-level idempotency only applies when the source supplies a dedup key;
-- keyless events (no natural identity) are never collapsed.
CREATE UNIQUE INDEX IF NOT EXISTS idx_events_dedup ON events(source, dedup_key) WHERE dedup_key != '';

-- Crash recovery scans unrouted events, so index the open set.
CREATE INDEX IF NOT EXISTS idx_events_unrouted ON events(routed_at) WHERE routed_at IS NULL;
