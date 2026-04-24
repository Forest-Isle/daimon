# 4-Layer Hierarchical Configuration

## Overview

Extends IronClaw's configuration system from single-file loading to a 4-layer hierarchy with deny-first rule merging. Higher-priority layers override lower ones, while deny rules at any level always take precedence over allow rules.

## Configuration Levels

```
Priority (highest → lowest):

L4: Local    .ironclaw/local.yaml         — Per-developer overrides (gitignored)
L3: Project  .ironclaw/ironclaw.yaml      — Project-specific settings
L2: User     ~/.ironclaw/config.yaml      — User defaults
L1: System   /etc/ironclaw/config.yaml    — Admin global rules
```

## Loading Process

`LoadHierarchy(workDir)` performs:

1. **Discover** — Checks all 4 paths for existence
2. **Load** — Reads each found file (YAML with `${ENV_VAR}` expansion)
3. **Merge** — Applies levels in ascending priority order (System → User → Project → Local)
4. **Return** — Merged config + list of discovered sources

```go
cfg, sources, err := config.LoadHierarchy("/path/to/project")
// sources shows which levels were found and loaded
```

## Merge Semantics

### Scalar Fields
Non-zero overlay values override base values. Zero/empty values are skipped (preserving lower-level settings).

```yaml
# ~/.ironclaw/config.yaml (User)
llm:
  provider: claude
  model: claude-sonnet-4-20250514

# .ironclaw/ironclaw.yaml (Project)
llm:
  model: claude-haiku-4-20250514  # overrides User level
  # provider not set → inherits "claude" from User level
```

### Slice Fields
Slices are **appended**, not replaced. This allows each level to add items:
- `agent.blocked_commands` — accumulated across levels
- `sandbox.allowed_dirs` / `sandbox.readonly_dirs` — accumulated
- `hooks.pre_tool_use` / `hooks.post_tool_use` — accumulated
- `agents.definitions` — accumulated

### Map Fields
MCP server maps are **merged** (per-key override):
```yaml
# User level
tools:
  mcp:
    servers:
      github: { command: "mcp-github" }

# Project level
tools:
  mcp:
    servers:
      database: { command: "mcp-postgres" }  # added
      github: { command: "mcp-github-v2" }   # overrides
```

### Permission Rules — Deny-First

Permission rules follow strict deny-first semantics:

```
If ANY level has action: "deny" for a (tool, pattern) key
  → merged result is ALWAYS "deny"
  → regardless of allow/ask at other levels

Otherwise:
  → highest priority level wins
```

```yaml
# System level (/etc/ironclaw/config.yaml)
permissions:
  rules:
    - tool: "bash"
      pattern: "rm -rf /"
      action: "deny"        # Admin blocks this globally

# Local level (.ironclaw/local.yaml)
permissions:
  rules:
    - tool: "bash"
      pattern: "rm -rf /"
      action: "allow"       # Developer tries to override
      # → DENIED: deny at System level always wins
```

## Project Instructions

`LoadProjectInstructions(workDir)` loads `.ironclaw/IRONCLAW.md` (or fallback `IRONCLAW.md` in project root) as project-level instructions. These are designed to be injected as **user context** (probabilistic compliance) rather than system prompt (deterministic).

## Files

| File | Lines | Description |
|---|---|---|
| `internal/config/hierarchy.go` | 391 | 4-layer loader + source discovery + field-by-field merge |
| `internal/config/rules.go` | 65 | Deny-first permission merge + project instructions loader |
| `internal/config/hierarchy_test.go` | 231 | 12 tests for discovery, merge, priority, MCP, slices |
| `internal/config/rules_test.go` | 135 | 9 tests for deny-first, priority, patterns, instructions |

## Testing

```bash
go test ./internal/config/...
# 21 tests pass
```
