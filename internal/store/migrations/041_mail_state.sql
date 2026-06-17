CREATE TABLE IF NOT EXISTS mail_state (
    mailbox      TEXT PRIMARY KEY,
    uid_validity INTEGER NOT NULL,
    last_uid     INTEGER NOT NULL,
    updated_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
