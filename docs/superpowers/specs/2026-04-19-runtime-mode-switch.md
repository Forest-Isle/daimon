# Runtime Mode Switch via /mode Command

## Overview

Allow users to switch between `simple` and `cognitive` agent modes at runtime using the `/mode` command, without restarting IronClaw. The switch takes effect on the next message.

## Architecture

### Gateway Changes

`Gateway` gains an `currentMode atomic.Value` (stores `string`) initialized from `cfg.Agent.Mode` at startup.

Both `runtime` (simple) and `cognitiveAgent` (cognitive) are **always initialized** at startup, regardless of the configured mode. The existing `if cfg.Agent.Mode == "cognitive"` guard in `initCognitiveAgent()` is removed.

Routing in `HandleInbound` changes from nil-check to mode-check:

```go
if gw.currentMode.Load().(string) == "cognitive" {
    gw.cognitiveAgent.HandleMessage(ctx, ch, msg)
} else {
    gw.runtime.HandleMessage(ctx, ch, msg)
}
```

`Gateway` exposes a `SetMode(mode string) error` method that validates the value and stores it atomically.

### Command Interception

`/mode` is intercepted in `HandleInbound` at the same level as `/new` and `/start`, before routing to any agent. This ensures all channels (TUI, Telegram) get identical behavior with no per-channel code.

### Command Syntax

```
/mode             — show current mode
/mode simple      — switch to simple
/mode cognitive   — switch to cognitive
```

### Response Messages

| Situation | Message |
|-----------|---------|
| Switch success | `✅ Mode switched to cognitive (was: simple)` |
| Already current | `ℹ️ Already in cognitive mode` |
| Invalid argument | `❌ Unknown mode "foo". Valid modes: simple, cognitive` |

### TUI Synchronization

`Model.agentMode` is a display-only field used by `/status` and `/version`. After a successful mode switch, the gateway sends the confirmation message through the channel; the TUI adapter detects the mode-switch response pattern and sends a `setAgentModeMsg` to the Bubble Tea program to update `m.agentMode`.

`/mode` is registered in `commandRegistry` in `commands.go` so TUI autocomplete includes it.

## Files Changed

| File | Change |
|------|--------|
| `internal/gateway/gateway.go` | Add `currentMode atomic.Value`; add `SetMode()`; intercept `/mode` in `HandleInbound`; change routing from nil-check to mode-check |
| `internal/gateway/init_agent.go` | Remove `if cfg.Agent.Mode == "cognitive"` guard; always init cognitive agent |
| `internal/channel/tui/adapter.go` | Detect mode-switch confirmation message; send `setAgentModeMsg` to sync `m.agentMode` |
| `internal/channel/tui/commands.go` | Register `/mode` command with `ArgHint: "[simple\|cognitive]"` |

## Edge Cases

- **In-flight requests**: A request already being processed by one agent completes with that agent. The new mode applies starting from the next message. No locking needed.
- **cognitiveAgent with missing OpenAI key**: Existing `noopKBEmbedder` fallback handles this; cognitive agent starts without vector search.
- **`/mode` with no arg**: Returns current mode (read-only, no side effects).
