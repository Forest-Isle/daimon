-- RL Episodes: training round records
CREATE TABLE IF NOT EXISTS rl_episodes (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    goal TEXT NOT NULL,
    complexity TEXT NOT NULL,
    total_reward REAL NOT NULL DEFAULT 0,
    succeeded INTEGER NOT NULL DEFAULT 0,
    subtask_count INTEGER NOT NULL DEFAULT 0,
    replan_count INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    metadata TEXT DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_rl_episodes_session ON rl_episodes(session_id);
CREATE INDEX IF NOT EXISTS idx_rl_episodes_created ON rl_episodes(created_at DESC);

-- RL Trajectories: state-action-reward traces
CREATE TABLE IF NOT EXISTS rl_trajectories (
    id TEXT PRIMARY KEY,
    episode_id TEXT NOT NULL,
    step INTEGER NOT NULL,
    level TEXT NOT NULL,  -- 'bandit', 'ppo', 'dqn'
    state BLOB NOT NULL,
    action BLOB NOT NULL,
    reward REAL NOT NULL DEFAULT 0,
    next_state BLOB,
    done INTEGER NOT NULL DEFAULT 0,
    metadata TEXT DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (episode_id) REFERENCES rl_episodes(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_rl_trajectories_episode ON rl_trajectories(episode_id);
CREATE INDEX IF NOT EXISTS idx_rl_trajectories_level ON rl_trajectories(level);

-- RL Rewards: multi-dimensional reward decomposition
CREATE TABLE IF NOT EXISTS rl_rewards (
    id TEXT PRIMARY KEY,
    episode_id TEXT NOT NULL,
    reward_type TEXT NOT NULL,  -- 'task_success', 'efficiency', 'safety', 'user_feedback'
    value REAL NOT NULL,
    weight REAL NOT NULL DEFAULT 1.0,
    metadata TEXT DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (episode_id) REFERENCES rl_episodes(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_rl_rewards_episode ON rl_rewards(episode_id);
CREATE INDEX IF NOT EXISTS idx_rl_rewards_type ON rl_rewards(reward_type);

-- RL Model Checkpoints: policy snapshots
CREATE TABLE IF NOT EXISTS rl_model_checkpoints (
    id TEXT PRIMARY KEY,
    policy_name TEXT NOT NULL,
    version INTEGER NOT NULL,
    state_dim INTEGER NOT NULL,
    action_dim INTEGER NOT NULL,
    weights BLOB NOT NULL,
    metrics TEXT DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(policy_name, version)
);

CREATE INDEX IF NOT EXISTS idx_rl_checkpoints_policy ON rl_model_checkpoints(policy_name);

-- Bandit arm statistics
CREATE TABLE IF NOT EXISTS rl_bandit_arms (
    id TEXT PRIMARY KEY,
    context_hash TEXT NOT NULL,
    arm_name TEXT NOT NULL,
    alpha REAL NOT NULL DEFAULT 1.0,
    beta REAL NOT NULL DEFAULT 1.0,
    pulls INTEGER NOT NULL DEFAULT 0,
    total_reward REAL NOT NULL DEFAULT 0,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(context_hash, arm_name)
);

CREATE INDEX IF NOT EXISTS idx_rl_bandit_context ON rl_bandit_arms(context_hash);
