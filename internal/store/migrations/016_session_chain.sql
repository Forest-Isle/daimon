-- Add parent_session_id column for session continuity chains.
-- When context compression triggers a new session, the old session ID is recorded
-- as parent_session_id, forming a linked chain back to the root session.
ALTER TABLE sessions ADD COLUMN parent_session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_sessions_parent ON sessions(parent_session_id);
