-- Migration 035: Track parent episode ids for subagent outcome journal rows.
ALTER TABLE journal ADD COLUMN parent_episode_id TEXT DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_journal_parent ON journal(parent_episode_id);
