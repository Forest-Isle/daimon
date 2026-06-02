-- 022_drop_rl_tables.sql: Remove RL system tables (RL code was removed in Phase 2 refactor).
-- Migration 007_rl_system.sql has been deleted; this cleans up any existing RL data.

DROP TABLE IF EXISTS rl_bandit_arms;
DROP TABLE IF EXISTS rl_model_checkpoints;
DROP TABLE IF EXISTS rl_rewards;
DROP TABLE IF EXISTS rl_trajectories;
DROP TABLE IF EXISTS rl_episodes;
