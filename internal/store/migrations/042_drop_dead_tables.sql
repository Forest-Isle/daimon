-- Drop orphan tables left over from early designs.
-- These tables have no production reads or writes; replay uses JSONL instead.
-- Forward migration: add a later migration to recreate them if needed.
DROP TABLE IF EXISTS reflection_tracker_state;
DROP TABLE IF EXISTS sidechain_entries;
DROP TABLE IF EXISTS execution_events;
DROP TABLE IF EXISTS agent_replays;
DROP TABLE IF EXISTS agent_replay_events;
