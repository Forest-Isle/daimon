CREATE TABLE IF NOT EXISTS sidechain_entries (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    parent_id TEXT NOT NULL DEFAULT '',
    chain_id TEXT NOT NULL DEFAULT '',
    entry_type TEXT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_sidechain_agent_id ON sidechain_entries(agent_id);
CREATE INDEX IF NOT EXISTS idx_sidechain_chain_id ON sidechain_entries(chain_id);
CREATE INDEX IF NOT EXISTS idx_sidechain_created_at ON sidechain_entries(created_at);
