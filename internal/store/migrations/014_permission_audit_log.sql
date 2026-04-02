-- Permission audit log for tracking all tool execution permission decisions
CREATE TABLE IF NOT EXISTS permission_audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    input_summary TEXT,
    action TEXT NOT NULL,
    matched_rule TEXT,
    reason TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_audit_session ON permission_audit_log(session_id);
CREATE INDEX IF NOT EXISTS idx_audit_tool ON permission_audit_log(tool_name);
