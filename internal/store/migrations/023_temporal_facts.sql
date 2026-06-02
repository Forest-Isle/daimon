-- 023_temporal_facts.sql: Add temporal validity to memory facts.
-- Every fact now has valid_from (when it became true) and valid_to (when it was superseded).
-- Soft invalidation: contradictions set valid_to without deleting the old fact.
-- Queries filter valid_to IS NULL by default for current-truth retrieval.

ALTER TABLE memory_index ADD COLUMN valid_from DATETIME;
ALTER TABLE memory_index ADD COLUMN valid_to DATETIME;

-- Backfill: existing facts get valid_from = created_at (best estimate).
UPDATE memory_index SET valid_from = created_at WHERE valid_from IS NULL;

-- Partial index: only active facts (valid_to IS NULL) are indexed for fast current-truth queries.
CREATE INDEX IF NOT EXISTS idx_memory_index_active ON memory_index(memory_id) WHERE valid_to IS NULL;
