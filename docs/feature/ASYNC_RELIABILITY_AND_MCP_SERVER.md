# Async Reliability Fixes & MCP Server Mode

## Overview

Two complementary improvements: (A) eliminates silent failures in async operations across the agent loop, and (B) adds MCP Server mode so IronClaw can serve as a persistent AI backend for IDEs and other MCP clients.

---

## Part A: Async Reliability

### Problem

Several critical operations ran as fire-and-forget goroutines with no timeout, confirmation, or error reporting:

1. **`reflect.go`** — Memory writes (`go func() { writeMemory(...) }()`) could silently fail or hang indefinitely
2. **`evolution/engine.go`** — Tool events dispatched "synchronously" but actually via async goroutines, creating a race with episode dispatch
3. **`cognitive.go`** — Session persisted before evolution hooks completed (`WaitPending()` never called)
4. **`rl/experience.go`** — Experience replay buffer grew unbounded

### Fixes

#### 1. Reflect Memory Writes — Timeout + Confirmation

**Before**: `go func() { doMemoryWrite(facts) }()`

**After**:
```go
writeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

done := make(chan error, 1)
go func() { done <- doMemoryWrite(writeCtx, facts) }()

select {
case err := <-done:
    if err != nil {
        slog.Warn("reflect: memory write failed", "err", err)
    }
case <-writeCtx.Done():
    slog.Warn("reflect: memory write timed out after 30s")
}
```

Key change: Memory write failure is now **logged and observable**, not silently swallowed. The 30s timeout prevents indefinite hangs from slow DB operations.

#### 2. Evolution Engine Event Ordering

The engine comment said tool events were "synchronous" but the implementation used `wg.Add(1)` + goroutines. Now tool events truly block before episode dispatch begins.

#### 3. WaitPending in Cognitive Loop

Added to `cognitive.go` before session persistence:
```go
if ca.evoEngine != nil {
    ca.evoEngine.WaitPending()
}
```

This ensures all evolution hooks (preference learning, skill synthesis, trajectory recording) complete before the session is saved to disk.

#### 4. Experience Buffer Cap

Added `maxBufferSize = 1000` constant to `experience.go`. When the buffer exceeds this limit, the oldest experiences are evicted (FIFO). Prevents memory leaks in long-running sessions.

### Files Changed

| File | Change |
|---|---|
| `internal/agent/reflect.go` | Timeout + confirmation for memory writes |
| `internal/evolution/engine.go` | (Already correct in current version) |
| `internal/agent/cognitive.go` | `WaitPending()` before session persist |
| `internal/rl/experience.go` | Buffer size cap with LRU eviction |

---

## Part B: MCP Server Mode

### Motivation

IronClaw currently only acts as an MCP **client** (connecting to external MCP tool servers). By also acting as an MCP **server**, IronClaw becomes persistent AI infrastructure:

- **IDEs** (VS Code, Cursor, Windsurf) can connect to IronClaw and access its accumulated memory and knowledge
- **Other agents** can delegate tasks to IronClaw's cognitive loop
- IronClaw transitions from "a single agent" to "a shared AI backend"

### Architecture

```
External Client (IDE / Agent)
    │
    │  JSON-RPC 2.0 (stdio or HTTP)
    │
    ▼
┌─────────────────────────┐
│   IronClaw MCP Server   │
│   (mark3labs/mcp-go)    │
├─────────────────────────┤
│  ironclaw_memory_search │ → memory.Store
│  ironclaw_knowledge_query│ → knowledge.Searcher
│  ironclaw_skill_list    │ → skill.Manager
└─────────────────────────┘
```

### Implementation

**`internal/mcp/server.go`** — Server wrapper:
```go
type Server struct {
    mcpServer *mcpserver.MCPServer
    deps      ServerDeps
}
```

- `NewServer(opts ...ServerOption)` — Creates server with `ironclaw/1.0.0` identity
- `ServeStdio(ctx)` — stdio transport (for IDE integration)
- `ServeHTTP(ctx, addr)` — Streamable HTTP transport (for remote access)
- `RegisterTool(tool, handler)` — Registers individual MCP tools

**`internal/mcp/server_tools.go`** — Tool registration:

```go
type ServerDeps struct {
    MemoryStore memory.Store        // nil = skip memory tools
    Knowledge   knowledge.Searcher  // nil = skip knowledge tools
    SkillMgr    *skill.Manager      // nil = skip skill tools
}
```

`RegisterDefaultTools(srv, deps)` registers available tools based on non-nil deps.

### Exposed Tools

| Tool | Description | Input |
|---|---|---|
| `ironclaw_memory_search` | Search IronClaw's accumulated memory | `query` (string, required), `limit` (number, default 5) |
| `ironclaw_knowledge_query` | Query the knowledge base | `query` (string, required), `limit` (number, default 5) |
| `ironclaw_skill_list` | List all available skills | (none) |

All tools return JSON-formatted results. Tools with nil dependencies are silently skipped during registration.

### Transport

| Mode | Command | Use Case |
|---|---|---|
| **stdio** | `ironclaw mcp serve` | IDE integration (VS Code, Cursor) |
| **HTTP** | `ironclaw mcp serve --http :8089` | Remote access, multi-client |

### Dependency

Uses the existing `github.com/mark3labs/mcp-go v0.44.0` dependency (already in go.mod for the MCP client). No new dependencies required.

### IDE Integration Example

**VS Code MCP config** (`.vscode/mcp.json`):
```json
{
  "mcpServers": {
    "ironclaw": {
      "command": "ironclaw",
      "args": ["mcp", "serve"]
    }
  }
}
```

**Cursor MCP config** (`.cursor/mcp.json`):
```json
{
  "mcpServers": {
    "ironclaw": {
      "command": "ironclaw",
      "args": ["mcp", "serve"]
    }
  }
}
```

### Design Decisions

1. **Curated tool set, not full exposure** — Following Hermes Agent's pattern: expose purpose-built tools, not all internal tools. This prevents accidental privilege escalation via MCP.

2. **Nil-safe deps** — Tools are only registered when their dependencies are available. A minimal IronClaw with no memory store simply offers fewer MCP tools.

3. **Stateless handlers** — Each tool call is independent; no session state maintained in MCP server. This simplifies multi-client scenarios.

### Files

| File | Lines | Description |
|---|---|---|
| `internal/mcp/server.go` | 59 | MCP server wrapper with stdio/HTTP transport |
| `internal/mcp/server_tools.go` | 174 | Tool registration + handlers for memory/knowledge/skills |

## Testing

```bash
go build ./...
go test ./internal/mcp/...
go test ./internal/agent/...
go test ./internal/rl/...
```
