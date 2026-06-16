-- Typed proposal actions. episode = fire ActionPlan as an episode goal (existing
-- behavior); promote_skill = action_ref stores the staged draft slug and accept
-- performs deterministic skill promotion. Defaults preserve historical proposal
-- behavior.
ALTER TABLE proposals ADD COLUMN action_kind TEXT NOT NULL DEFAULT 'episode';
ALTER TABLE proposals ADD COLUMN action_ref  TEXT NOT NULL DEFAULT '';
