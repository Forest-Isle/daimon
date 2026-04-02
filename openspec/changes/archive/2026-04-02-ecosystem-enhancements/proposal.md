## Why

IronClaw's agent loop currently operates without awareness of the user's working environment (git state, directory context, recent file changes) unless the user explicitly mentions it. Additionally, all permission decisions are ephemeral — there's no audit trail of what was allowed, denied, or auto-approved. Claude Code injects environment context automatically (git branch, status, recent changes) before each agent turn, and logs every permission decision with structured reason codes. Adding context injectors and permission audit logging to IronClaw will make the agent more context-aware and the security model more transparent, especially valuable for the cognitive agent's PERCEIVE phase which explicitly gathers environmental information.

## What Changes

- **Context injectors**: A `ContextInjector` interface that auto-injects environment context (git status, current directory, recent file changes) into the system prompt. Injectors are registered and configured via YAML. Built-in implementations include `GitContextInjector` and `WorkdirContextInjector`. Integrates naturally with Phase 2's `OnUserMessage` hook event.
- **Permission audit logging**: Every permission decision (allow, deny, ask → approved/denied) is recorded with structured metadata: tool name, input summary, matched rule, decision reason, timestamp. Stored in SQLite for querying. Integrates as a built-in `PostToolUse` hook handler from Phase 2.

## Capabilities

### New Capabilities
- `context-injectors`: Pluggable environment context injection into the agent loop's system prompt, with built-in Git and workdir injectors.
- `permission-audit-logging`: Structured logging of all permission decisions to SQLite, queryable for debugging and compliance.

### Modified Capabilities
<!-- No existing spec-level requirements are changing. -->

## Impact

- **`internal/hook/`**: New built-in handlers: `ContextInjectorHandler` (OnUserMessage), `PermissionAuditHandler` (PostToolUse).
- **`internal/agent/runtime.go`**: System prompt builder uses injected context from OnUserMessage hooks.
- **`internal/agent/cognitive.go`**: PERCEIVE phase benefits from auto-injected context.
- **`internal/store/migrations/`**: New migration (007) for `permission_audit_log` table.
- **Configuration**: New sections under `hooks.on_user_message` for context injectors and `hooks.post_tool_use` for audit logging.
- **Dependencies**: No new external dependencies. Git context uses `os/exec` to call `git` commands.
