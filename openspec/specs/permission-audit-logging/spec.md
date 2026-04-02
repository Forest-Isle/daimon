## ADDED Requirements

### Requirement: Permission decision recording
Every tool permission decision SHALL be recorded to a `permission_audit_log` SQLite table with structured metadata including session ID, tool name, input summary, action taken, matched rule, and reason code.

#### Scenario: Tool allowed by rule
- **WHEN** a tool execution is allowed by a matching permission rule
- **THEN** a record SHALL be written with `action: "allow"`, the matched rule description, and `reason: "rule_match"`

#### Scenario: Tool denied by rule
- **WHEN** a tool execution is denied by a matching permission rule
- **THEN** a record SHALL be written with `action: "deny"`, the matched rule description, and `reason: "rule_match"`

#### Scenario: Tool approved by user
- **WHEN** a tool execution triggers user approval and the user approves
- **THEN** a record SHALL be written with `action: "ask_approved"` and `reason: "user_approval"`

#### Scenario: Tool denied by user
- **WHEN** a tool execution triggers user approval and the user denies
- **THEN** a record SHALL be written with `action: "ask_denied"` and `reason: "user_approval"`

#### Scenario: Tool denied by hook
- **WHEN** a PreToolUse hook handler denies the execution
- **THEN** a record SHALL be written with `action: "deny"` and `reason: "hook_deny"`

### Requirement: Audit log as PostToolUse handler
Permission audit logging SHALL be implemented as a built-in `PostToolUseHandler` from the Phase 2 hook system. It SHALL receive the full execution context including the permission decision.

#### Scenario: Handler enabled
- **WHEN** `{type: audit_logger}` is configured under `hooks.post_tool_use`
- **THEN** every tool execution SHALL produce an audit log entry

#### Scenario: Handler not configured
- **WHEN** no audit logger handler is configured
- **THEN** no audit log entries SHALL be written (zero overhead)

### Requirement: Input summary truncation
The `input_summary` field in the audit log SHALL contain at most the first 200 characters of the tool input to prevent excessive storage consumption.

#### Scenario: Long input
- **WHEN** a tool input is 5000 characters long
- **THEN** the `input_summary` SHALL contain the first 200 characters followed by "..."

### Requirement: Audit log database migration
The `permission_audit_log` table SHALL be created via a new SQLite migration (007) with indexes on `session_id` and `tool_name` for efficient querying.

#### Scenario: Migration applied
- **WHEN** IronClaw starts with the new migration
- **THEN** the `permission_audit_log` table and its indexes SHALL be created if they do not already exist
