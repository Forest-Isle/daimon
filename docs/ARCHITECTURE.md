# IronClaw Architecture

## Two Runtimes

IronClaw has two agentic runtimes living side-by-side:

| | Legacy (`internal/agent`) | Core (`internal/core`) |
|---|---|---|
| Entry | `ironclaw start` (Gateway) | `ironclaw core run` |
| Loop | 1560-line CognitiveAgent | 200-line Agent.Step |
| Wiring | God-object Gateway (~1200 lines) | boot.New (~100 lines) |
| Extensibility | Edit Gateway + cognitive.go | Middleware chain |
| Persistence | SQLite + Markdown files | Pluggable Memory interface |
| Channels | Telegram/TUI/Discord | Not yet (embedded only) |
| MCP | Gateway goroutines | Not yet |

Legacy is feature-rich but nearly impossible to follow. Core is minimal but composable.

## Core Architecture

```
┌─────────────────────────────────────────────────────┐
│                   boot.New(cfg)                     │
│  Config → Provider + Tools → Agent                  │
└─────────────────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────┐
│                     Agent                           │
│                                                     │
│  Run(prompt):                                       │
│    memory.Append(user msg)                          │
│    for turn in 1..MaxTurns:                         │
│      history ← memory.Snapshot()                    │
│      resp ← provider.Complete(history, tools)       │
│      memory.Append(assistant msg + tool_calls)      │
│      if no tool_calls: return resp.Text             │
│      results ← runToolBatch(tool_calls)             │
│      memory.Append(tool results)                    │
│                                                     │
│  Events emitted on EventSink at every boundary.     │
└─────────────────────────────────────────────────────┘
                         │
          ┌──────────────┼──────────────┐
          ▼              ▼              ▼
     Provider      ToolRegistry      Memory
   (core.Provider)  (core.Tool)   (core.Memory)
          │              │              │
     adapter/       adapter/       core.NewInMemory
   LegacyProvider  LegacyTool     (or custom impl)
```

### Package Map

```
internal/core/
├── doc.go              Package manifesto
├── types.go            Message, ToolCall, ToolResult, StopReason
├── provider.go         LLMRequest/Response, Provider, Stream interfaces
├── tools.go            Tool, ToolSchema, ToolFunc, ToolRegistry
├── events.go           Event, EventKind, EventSink, MultiSink
├── policy.go           Gate, Approver, Decision, AllowAllGate
├── middleware.go        ToolHandler, ToolMiddleware, chainTool
├── agent.go            Agent, Config, InMemory, runToolBatch
├── builtin_middleware.go  GateMiddleware, CacheMiddleware, TraceMiddleware, TimeoutMiddleware
│
├── adapter/
│   ├── adapter.go      LegacyProvider, LegacyTool, ImportToolRegistry
│   └── adapter_test.go
│
└── boot/
    ├── boot.go         New(cfg) → *Agent, Run(cfg, prompt)
    └── boot_test.go
```

### Design Principles

1. **Agent is a small struct, not a god object.** The `Agent` type has 4 fields: config, provider, tools, memory, handler. Adding behaviour means composing ToolMiddleware or wrapping the Provider — never mutating Agent itself.

2. **Middleware is the extension point.** Gate (permissions), Cache (dedup), Trace (telemetry), Timeout (safety), Retry (resilience) — all implemented as `ToolMiddleware`. Order is explicit in `Config.ToolMiddleware`.

3. **Events are typed but payload is any.** Consumers switch on `EventKind` then type-assert payload. This keeps the bus allocation-free for fast paths (chunks) while remaining expressive.

4. **Provider is the only network boundary.** The core never touches HTTP, SQLite, or files. The `Provider` interface is intentionally minimal: `Complete` + `Stream`. The adapter layer bridges to the legacy `agent.Provider` without copying code.

5. **Memory is pluggable.** `InMemory` for tests and one-shots. A `SQLiteMemory` or `FileMemory` implementing the same 2-method interface (`Append`, `Snapshot`) can be dropped in.

6. **Errors are evidence, not exceptions.** When a tool fails or is denied by policy, the error is surfaced as a tool result message — the model can observe and recover. The loop only hard-stops on provider failures or max-turns.

### Adding a New Tool

```go
// Step 1: Implement core.Tool
type MyTool struct{}
func (t *MyTool) Schema() core.ToolSchema { ... }
func (t *MyTool) ReadOnly() bool { return false }
func (t *MyTool) Execute(ctx context.Context, input json.RawMessage) (core.ToolResult, error) { ... }

// Step 2: Register in boot or inline
reg.Register(&MyTool{})

// Step 3: Done. The Agent picks up all registered tools automatically.
```

### Adding a New Middleware

```go
func AuditMiddleware(logger *slog.Logger) core.ToolMiddleware {
    return func(next core.ToolHandler) core.ToolHandler {
        return func(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
            logger.Info("tool called", "name", call.Name)
            return next(ctx, call)
        }
    }
}
// Then: Config{ToolMiddleware: []core.ToolMiddleware{AuditMiddleware(log)}}
```

### CLI

```bash
# Interactive: list tools available in the core agent
ironclaw core tools

# One-shot: run a prompt through the clean loop
ironclaw core run "what files are in the current directory?"

# Verbose event stream
ironclaw core run -v "list files"

# JSON event stream (for piping to jq or a dashboard)
ironclaw core run --json "find TODO items" 2>events.jsonl
```

### Test Coverage

```
internal/core/          7 tests: single-turn, tool roundtrip, parallel batch,
                         gate denial, max-turns guard, cache dedup, event ordering
internal/core/adapter/  1 test: legacy provider + legacy tool → core.Agent roundtrip
internal/core/boot/     4 tests: New builds, ListToolSchemas, nil config guard,
                         unknown provider guard
```

### Legacy Gateway (fixed)

Two bugs were fixed in `internal/gateway/gateway.go`:
1. `Start()` had duplicated `gw.wasmHost.Close(ctx)` calls and broken brace structure that accidentally compiled but leaked a dangling `gw.wasmHost.Close` before the RL trainer.
2. `Stop()` had a redundant blank line and duplicated wasmHost.Close.

The legacy Gateway continues to work unchanged for `ironclaw start` + Telegram/TUI channels.
