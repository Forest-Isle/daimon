-- Migration 036: Track when a proposal was delivered to the user (DAIMON_BLUEPRINT.md
-- §4.9). The delivery driver pushes only undelivered pending proposals and stamps
-- delivered_at after a successful send, so a proposal is offered to the user once
-- rather than re-pushed every sleep/delivery cycle. 0 = not yet delivered.
ALTER TABLE proposals ADD COLUMN delivered_at INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_proposals_undelivered ON proposals(state, delivered_at);
