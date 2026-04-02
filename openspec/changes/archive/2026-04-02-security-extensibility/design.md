## Context

IronClaw's security model consists of:
1. A `Policy` struct in `internal/tool/policy.go` with a hardcoded `blockedCommands []string` that uses `strings.Contains` matching against bash commands
2. A `RequiresApproval() bool` method on the `Tool` interface that returns a static boolean per tool type
3. An `ApprovalFunc` callback in `runtime.go` that delegates approval UI to the channel adapter

This is functional but limited: users cannot customize security rules per-project, there are no extension points for pre/post tool behavior, and tool results carry no semantic metadata. The lack of extensibility means features like audit logging, safety analysis, and context injection must be hardcoded into the runtime.

## Goals / Non-Goals

**Goals:**
- Enable user-configurable permission rules that replace the hardcoded blocklist
- Provide a plugin-like hook system with well-defined lifecycle events
- Enrich tool results with metadata that downstream systems can use for smarter handling
- Maintain the simplicity of the current `Tool` interface (new features via optional interfaces)
- Keep the hook system lightweight — Go interfaces, not a full plugin framework

**Non-Goals:**
- External plugin loading (shared libraries, WASM) — hooks are compiled-in Go interfaces
- Per-user permission profiles (future work — current system is single-user)
- Real-time permission rule hot-reload (requires restart)
- UI for managing permissions (YAML config is the interface)
- Modifying MCP tool adapter behavior (MCP tools opt into the same interfaces)

## Decisions

### Decision 1: Rule-based permissions with wildcard matching

**Choice**: Replace `Policy.CheckBashCommand()` with a `PermissionEngine` that evaluates rules from YAML config. Rules specify tool name, pattern, and action (allow/deny/ask). Patterns use glob-style matching (`git *`, `rm -rf *`).

**Alternatives considered**:
- *AST-based command parsing* (like Claude Code's `parseForSecurity`): More accurate but complex. Go has no built-in shell AST parser. Rejected for v1 — glob matching covers 90% of cases.
- *Regex-based patterns*: More powerful but harder for users to write correctly. Rejected — glob is more intuitive.
- *Keep strings.Contains*: Current approach. Rejected — too many false positives (e.g., blocking "rm" also blocks "format").

**Rule evaluation order**: Rules are evaluated top-to-bottom. First match wins. If no rule matches, the default action applies (configurable, defaults to `ask`).

```yaml
permissions:
  default: ask
  rules:
    - tool: bash
      pattern: "git *"
      action: allow
    - tool: bash
      pattern: "rm -rf *"
      action: deny
    - tool: file_write
      path_pattern: "/etc/*"
      action: deny
    - tool: "*"
      action: ask  # fallback
```

### Decision 2: Optional `CapableTool` interface for rich capability flags

**Choice**: Define `CapableTool` interface with `Capabilities() ToolCapabilities` method. `ToolCapabilities` struct contains `IsReadOnly`, `IsDestructive`, `RequiresNetwork`, and `ApprovalMode` fields. Tools that don't implement it get safe defaults.

This subsumes the `ReadOnlyTool` interface from Phase 1 — `CapableTool` is a superset. Phase 1 can ship with `ReadOnlyTool` first; Phase 2 migrates to `CapableTool` and `ReadOnlyTool` becomes a convenience adapter.

### Decision 3: HookManager with typed event handlers

**Choice**: A `HookManager` dispatches events to registered handlers. Handlers are Go interfaces, one per event type. Registration is done at gateway initialization time based on YAML config.

```go
type PreToolUseHandler interface {
    OnPreToolUse(ctx context.Context, event PreToolUseEvent) (PreToolUseResult, error)
}

type PreToolUseResult struct {
    Action   string // "allow", "deny", "ask", "passthrough"
    Reason   string
    Modified *json.RawMessage // optionally modify tool input
}
```

**Event types**:
- `PreToolUse`: Before tool execution. Can allow/deny/modify input.
- `PostToolUse`: After tool execution. Can transform output, log, alert.
- `OnUserMessage`: After user message received. Can inject context, validate input.
- `PreCompact`: Before context compression. Can preserve critical information.

**Alternatives considered**:
- *Channel-based pub/sub*: More decoupled but adds async complexity. Rejected for synchronous lifecycle events.
- *Middleware chain (like HTTP middleware)*: Would work but feels over-engineered for 4 event types. Rejected.
- *Single `Hook` interface with event type switch*: Less type-safe. Rejected — Go's type system should work for us.

### Decision 4: Structured Result with backward-compatible extension

**Choice**: Extend `tool.Result` struct with optional metadata fields. Existing code that only reads `Output` and `Error` continues to work unchanged.

```go
type Result struct {
    Output   string            // unchanged
    Error    string            // unchanged
    Type     ResultType        // "text" (default), "image", "file", "reference"
    FilePath string            // path to associated file (if any)
    IsPartial bool             // true if output was truncated
    Metadata map[string]any    // extensible key-value pairs
}
```

**Alternatives considered**:
- *New `RichResult` type alongside `Result`*: Breaks the Tool interface. Rejected.
- *Interface-based result*: Over-abstraction for a data struct. Rejected.

## Risks / Trade-offs

**[Risk] Permission rules may have unintended interactions** → Mitigation: First-match-wins evaluation is predictable. A `--dry-run` flag on the CLI shows which rule would match for a given tool+input without executing. Rules are logged at startup.

**[Risk] Hook handlers may add latency to every tool call** → Mitigation: Handlers execute synchronously but are expected to be fast (< 10ms). PostToolUse handlers can opt into async execution. The hook system adds zero overhead when no handlers are registered.

**[Risk] Migration from Policy to PermissionEngine** → Mitigation: If no `permissions.rules` are configured, the system falls back to the legacy `Policy` blocklist behavior. This is a seamless upgrade path.

**[Trade-off] More configuration surface area** → Accepted: The defaults are secure (deny destructive by default, ask for unknown). Users who don't configure anything get the current behavior.
