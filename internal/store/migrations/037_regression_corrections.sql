-- Migration 037: Track user-corrected replay sessions for the must-pass
-- regression set (DAIMON_BLUEPRINT.md §4.10 mode 2).
CREATE TABLE regression_corrections (
    session_id TEXT PRIMARY KEY,
    note       TEXT,
    created_at INTEGER NOT NULL
);
