-- Persist previous_summary so incremental compression survives restarts.
ALTER TABLE sessions ADD COLUMN previous_summary TEXT DEFAULT '';
