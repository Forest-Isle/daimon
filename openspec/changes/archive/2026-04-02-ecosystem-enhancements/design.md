## Context

IronClaw's system prompt is built in `runtime.go:buildSystemPrompt()` from static sources: personality (Soul.md), core prompt (Agent.md), persistent rules (Memory.md), retrieved memories, user profile, skills, and agents. None of these include real-time environment context — the LLM doesn't know what git branch the user is on, what directory they're working in, or what files changed recently unless explicitly told. Meanwhile, permission decisions (approve/deny) are logged only in tool execution logs as a status string, with no structured audit trail.

Phase 2 introduces the hook/event system with `OnUserMessage` and `PostToolUse` events. Phase 3 leverages these hooks to provide two built-in capabilities: context injection and permission audit logging. Both are implemented as hook handlers, requiring no changes to the core runtime beyond what Phase 2 already provides.

## Goals / Non-Goals

**Goals:**
- Automatically inject git status, current working directory, and recent changes into the system prompt at each agent turn
- Record every permission decision with structured metadata to SQLite for later querying
- Make both features opt-in via YAML configuration (disabled by default to avoid surprises)
- Integrate seamlessly with Phase 2's hook system as built-in handlers

**Non-Goals:**
- Custom/user-defined injectors via external scripts (future work)
- Real-time permission monitoring dashboard or alerting
- Injecting context from non-Git VCS systems (Git only for v1)
- Exposing audit logs via API (SQLite queries only for now)

## Decisions

### Decision 1: Context injectors as OnUserMessage hook handlers

**Choice**: Implement `ContextInjector` as a specialized `OnUserMessageHandler` that returns context strings to be appended to the system prompt. Each injector is independent and configurable.

**Built-in injectors**:
- `GitContextInjector`: Runs `git branch --show-current` and `git status --short` (with configurable timeout, default 2s). Skipped if not in a git repository.
- `WorkdirContextInjector`: Includes current working directory and optionally a `ls` summary of recent files.

**Alternatives considered**:
- *Hardcode into `buildSystemPrompt()`*: Works but not extensible. Rejected — the hook system exists precisely for this.
- *Separate `ContextInjector` interface outside hooks*: Adds another abstraction layer. Rejected — OnUserMessage hook is the natural fit.

**Configuration**:
```yaml
hooks:
  on_user_message:
    - type: git_context
      config:
        timeout_ms: 2000
        include_diff_stat: false  # include --stat for recent commits
    - type: workdir_context
      config:
        include_ls: true
        max_files: 20
```

### Decision 2: Permission audit log in SQLite

**Choice**: Create a `permission_audit_log` table via migration 007. A built-in `PermissionAuditHandler` (PostToolUse handler) writes a row for every tool execution with the permission decision context.

**Schema**:
```sql
CREATE TABLE IF NOT EXISTS permission_audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    input_summary TEXT,        -- first 200 chars of input
    action TEXT NOT NULL,       -- 'allow', 'deny', 'ask_approved', 'ask_denied'
    matched_rule TEXT,          -- which rule matched (if any)
    reason TEXT,                -- 'rule_match', 'capability_default', 'user_approval', 'legacy_policy', 'hook_deny'
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_audit_session ON permission_audit_log(session_id);
CREATE INDEX IF NOT EXISTS idx_audit_tool ON permission_audit_log(tool_name);
```

**Alternatives considered**:
- *Log file (structured JSON)*: Harder to query, needs log rotation. Rejected — SQLite is already the persistence layer.
- *In-memory only*: Lost on restart. Rejected — audit logs should be persistent.
- *Shared table with `tool_execution_log`*: Different schema needs (execution log has output/duration, audit log has rules/reasons). Rejected — separate tables are cleaner.

### Decision 3: Cognitive agent PERCEIVE phase integration

**Choice**: The cognitive agent's PERCEIVE phase already gathers context. Context injectors augment this by providing environment context that the PERCEIVE phase can reference. No changes needed to cognitive.go — the injected context flows naturally through the system prompt that PERCEIVE reads.

## Risks / Trade-offs

**[Risk] Git commands may be slow on large repositories** → Mitigation: Configurable timeout (default 2s). If timeout is hit, injector returns empty string and logs a warning. Never blocks the agent loop.

**[Risk] Audit log table may grow large over time** → Mitigation: Recommend periodic cleanup (e.g., delete entries older than 30 days). Add a CLI command `ironclaw audit cleanup --older-than 30d` in a future iteration.

**[Trade-off] Disabled by default** → Accepted: Both features require explicit opt-in via hook configuration. This prevents surprise behavior changes but means users must know about them. Document in example config with clear comments.
