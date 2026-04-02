## Why

IronClaw's current security model relies on a hardcoded bash command blocklist (`internal/tool/policy.go`) and a binary `RequiresApproval() bool` on each tool. There is no way for users to configure permission rules per-project, no hook system for extending behavior before/after tool execution, and tool results carry no metadata beyond a plain string. Analysis of Claude Code reveals a sophisticated multi-layer permission system (rules from multiple sources, wildcard matching, decision logging) and a 4-event hook architecture that enables pluggable safety analyzers, context injectors, and audit loggers. Adopting configurable permissions, an event/hook system, and structured tool results will make IronClaw significantly more extensible and secure without sacrificing its simplicity.

## What Changes

- **Multi-layer permission system**: Replace hardcoded blocklist + boolean approval with configurable permission rules (YAML-driven, per-tool, wildcard pattern matching). Add `ToolCapabilities` struct with `IsReadOnly`, `IsDestructive`, `RequiresNetwork` flags. Permission rules sourced from global config, project config, and runtime overrides.
- **Hook/event system**: Introduce a `HookManager` with 4 event points: `PreToolUse`, `PostToolUse`, `OnUserMessage`, `PreCompact`. Handlers are Go interfaces registered via YAML config. Built-in handlers include `SafetyAnalyzer` (replaces Policy), `AuditLogger`, and `ContextInjector`.
- **Structured tool results**: Extend `tool.Result` with metadata fields: `Type` (text/image/file/reference), `FilePath`, `IsPartial`, `TokenEstimate`. Enables smarter result handling in compression, display, and context management.

## Capabilities

### New Capabilities
- `multi-layer-permissions`: Configurable permission rules with wildcard patterns, per-tool capability flags, and multi-source rule merging (global + project + session).
- `hook-event-system`: Pluggable event hooks at 4 lifecycle points with YAML-configurable handler chains and built-in implementations.
- `structured-tool-results`: Rich metadata on tool results enabling type-aware handling across the system.

### Modified Capabilities
<!-- No existing spec-level requirements are changing. The tool approval flow changes implementation but not external behavior. -->

## Impact

- **`internal/tool/tool.go`**: `Result` struct extended with metadata fields. New `ToolCapabilities` struct. New optional `CapableTool` interface.
- **`internal/tool/policy.go`**: Replaced by `internal/tool/permissions.go` with rule-based matching. Old `Policy` preserved as adapter for backward compatibility.
- **`internal/hook/`**: New package with `HookManager`, `HookHandler` interface, event types, and built-in handlers.
- **`internal/agent/runtime.go`**: Tool execution calls hooks before/after. Message handling calls `OnUserMessage` hook.
- **`internal/gateway/gateway.go`**: Wires HookManager into the initialization sequence.
- **Configuration**: New YAML sections: `permissions.rules`, `hooks.pre_tool_use`, `hooks.post_tool_use`, `hooks.on_user_message`.
- **Dependencies**: No new external dependencies.
