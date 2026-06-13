-- Migration 032: FTS5 search indexes for world retrieval.
-- The world model (journal + commitments) becomes searchable so episodes can
-- retrieve relevant past decisions/outcomes/promises instead of relying on the
-- legacy memory package. Standalone FTS5 tables keyed by the row's TEXT id (not
-- external-content, which needs an integer rowid), kept in sync by triggers.

CREATE VIRTUAL TABLE IF NOT EXISTS journal_fts USING fts5(
    journal_id UNINDEXED,
    summary,
    detail,
    tokenize = 'porter unicode61'
);

CREATE VIRTUAL TABLE IF NOT EXISTS commitments_fts USING fts5(
    commitment_id UNINDEXED,
    title,
    body,
    tokenize = 'porter unicode61'
);

-- Keep journal_fts in sync with journal.
CREATE TRIGGER IF NOT EXISTS journal_fts_ai AFTER INSERT ON journal BEGIN
    INSERT INTO journal_fts(journal_id, summary, detail) VALUES (new.id, new.summary, new.detail);
END;
CREATE TRIGGER IF NOT EXISTS journal_fts_ad AFTER DELETE ON journal BEGIN
    DELETE FROM journal_fts WHERE journal_id = old.id;
END;
CREATE TRIGGER IF NOT EXISTS journal_fts_au AFTER UPDATE ON journal BEGIN
    DELETE FROM journal_fts WHERE journal_id = old.id;
    INSERT INTO journal_fts(journal_id, summary, detail) VALUES (new.id, new.summary, new.detail);
END;

-- Keep commitments_fts in sync with commitments.
CREATE TRIGGER IF NOT EXISTS commitments_fts_ai AFTER INSERT ON commitments BEGIN
    INSERT INTO commitments_fts(commitment_id, title, body) VALUES (new.id, new.title, new.body);
END;
CREATE TRIGGER IF NOT EXISTS commitments_fts_ad AFTER DELETE ON commitments BEGIN
    DELETE FROM commitments_fts WHERE commitment_id = old.id;
END;
CREATE TRIGGER IF NOT EXISTS commitments_fts_au AFTER UPDATE ON commitments BEGIN
    DELETE FROM commitments_fts WHERE commitment_id = old.id;
    INSERT INTO commitments_fts(commitment_id, title, body) VALUES (new.id, new.title, new.body);
END;

-- Backfill any rows that predate this migration (no-op on a fresh database).
INSERT INTO journal_fts(journal_id, summary, detail)
    SELECT id, summary, detail FROM journal;
INSERT INTO commitments_fts(commitment_id, title, body)
    SELECT id, title, body FROM commitments;
