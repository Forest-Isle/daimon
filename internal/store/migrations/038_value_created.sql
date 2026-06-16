-- §4.11 episode self-reported value, written atomically with the outcome row.
-- Default 0 means no value was reported.
ALTER TABLE journal ADD COLUMN value_created_usd REAL NOT NULL DEFAULT 0;
