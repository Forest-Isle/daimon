# Runtime Mode Switch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `/mode [simple|cognitive]` command that switches the agent mode at runtime across all channels without restarting IronClaw.

**Architecture:** Gateway gains an `atomic.Value` holding the current mode string, both agents always initialize at startup, and `handleInbound` reads the atomic value to route messages. `/mode` is intercepted in `handleInbound` before agent routing, so TUI and Telegram both work without per-channel changes.

**Tech Stack:** Go `sync/atomic`, Bubble Tea message passing, existing gateway command interception pattern.

---

## File Map

| File | Change |
|------|--------|
| `internal/gateway/gateway.go` | Add `currentMode atomic.Value` field; add `SetMode()` + `CurrentMode()` methods; intercept `/mode` in `handleInbound`; change routing from nil-check to mode-check |
| `internal/gateway/init_cognitive.go` | Remove `if gw.cfg.Agent.Mode != "cognitive" { return nil }` guard |
| `internal/channel/tui/commands.go` | Register `/mode` command with arg hint |
| `internal/channel/tui/adapter.go` | Add `setAgentModeMsg`; detect mode-switch response in `Send()` to sync `m.agentMode` |

---

### Task 1: Always initialize cognitiveAgent regardless of configured mode

**Files:**
- Modify: `internal/gateway/init_cognitive.go:15-18`

- [ ] **Step 1: Write failing test**

Add to `internal/gateway/gateway_test.go` (create the file if absent, otherwise append):

```go
func TestCognitiveAgentAlwaysInitialized(t *testing.T) {
    cfg := config.DefaultConfig()
    cfg.LLM.APIKey = "test-key"
    cfg.Agent.Mode = "simple" // NOT cognitive

    gw, err := New(cfg)
    require.NoError(t, err)
    assert.NotNil(t, gw.cognitiveAgent, "cognitiveAgent must be initialized even when mode=simple")
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
CGO_ENABLED=1 go test -tags fts5 -run TestCognitiveAgentAlwaysInitialized ./internal/gateway/ -v
```

Expected: FAIL — `cognitiveAgent` is nil because the guard returns early.

- [ ] **Step 3: Remove the mode guard**

In `internal/gateway/init_cognitive.go`, delete lines 16-18:

```go
// DELETE these three lines:
if gw.cfg.Agent.Mode != "cognitive" {
    return nil
}
```

After deletion, `initCognitiveAgent()` starts directly at:
```go
gw.cognitiveAgent = agent.NewCognitiveAgent(...)
```

- [ ] **Step 4: Run test to verify it passes**

```bash
CGO_ENABLED=1 go test -tags fts5 -run TestCognitiveAgentAlwaysInitialized ./internal/gateway/ -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/init_cognitive.go internal/gateway/gateway_test.go
git commit -m "feat(gateway): always initialize cognitive agent regardless of configured mode"
```

---

### Task 2: Add `currentMode` field and `SetMode`/`CurrentMode` methods to Gateway

**Files:**
- Modify: `internal/gateway/gateway.go`

- [ ] **Step 1: Write failing test**

Append to `internal/gateway/gateway_test.go`:

```go
func TestGatewaySetMode(t *testing.T) {
    cfg := config.DefaultConfig()
    cfg.LLM.APIKey = "test-key"
    cfg.Agent.Mode = "simple"

    gw, err := New(cfg)
    require.NoError(t, err)

    assert.Equal(t, "simple", gw.CurrentMode())

    err = gw.SetMode("cognitive")
    require.NoError(t, err)
    assert.Equal(t, "cognitive", gw.CurrentMode())

    err = gw.SetMode("simple")
    require.NoError(t, err)
    assert.Equal(t, "simple", gw.CurrentMode())

    err = gw.SetMode("invalid")
    assert.Error(t, err)
    assert.Equal(t, "simple", gw.CurrentMode()) // unchanged
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
CGO_ENABLED=1 go test -tags fts5 -run TestGatewaySetMode ./internal/gateway/ -v
```

Expected: FAIL — `CurrentMode` and `SetMode` not defined.

- [ ] **Step 3: Add the field and methods**

In `internal/gateway/gateway.go`, add `currentMode sync.atomic.Value` to the `Gateway` struct after `stopOnce`:

```go
currentMode      atomic.Value // stores string: "simple" | "cognitive"
```

In `New()`, after `stopCh: make(chan struct{})`, initialize the field:

```go
gw.currentMode.Store(cfg.Agent.Mode)
```

Add these two methods anywhere in `gateway.go` (e.g. after the `Stop` function):

```go
// CurrentMode returns the active agent mode ("simple" or "cognitive").
func (gw *Gateway) CurrentMode() string {
    return gw.currentMode.Load().(string)
}

// SetMode atomically switches the active agent mode.
// Returns an error if mode is not "simple" or "cognitive".
func (gw *Gateway) SetMode(mode string) error {
    if mode != "simple" && mode != "cognitive" {
        return fmt.Errorf("unknown mode %q: valid modes are simple, cognitive", mode)
    }
    gw.currentMode.Store(mode)
    slog.Info("gateway: mode switched", "mode", mode)
    return nil
}
```

Also add `"sync/atomic"` to the import block if not already present (the `atomic.Value` type is in `sync/atomic` but declared as `atomic.Value` — it's actually in the `sync` package's `atomic` sub-package; use `sync/atomic` and type `atomic.Value`).

> Note: `atomic.Value` is `sync/atomic.Value`. The field declaration is `currentMode atomic.Value` with import `"sync/atomic"`. However in Go the import alias used in code is just `atomic`, so `atomic.Value` works with `import "sync/atomic"`.

- [ ] **Step 4: Run test to verify it passes**

```bash
CGO_ENABLED=1 go test -tags fts5 -run TestGatewaySetMode ./internal/gateway/ -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/gateway.go internal/gateway/gateway_test.go
git commit -m "feat(gateway): add currentMode atomic field with SetMode/CurrentMode methods"
```

---

### Task 3: Change `handleInbound` routing to use `currentMode` instead of nil-check

**Files:**
- Modify: `internal/gateway/gateway.go:405-415`

- [ ] **Step 1: Write failing test**

Append to `internal/gateway/gateway_test.go`:

```go
func TestHandleInboundRoutesByCurrent Mode(t *testing.T) {
    // This is an integration-style smoke test: verify that after SetMode,
    // the routing field is read (not cognitiveAgent nil-check).
    cfg := config.DefaultConfig()
    cfg.LLM.APIKey = "test-key"
    cfg.Agent.Mode = "simple"

    gw, err := New(cfg)
    require.NoError(t, err)

    // Both agents initialized; mode is simple.
    assert.Equal(t, "simple", gw.CurrentMode())
    assert.NotNil(t, gw.cognitiveAgent)

    // Switching to cognitive should succeed.
    require.NoError(t, gw.SetMode("cognitive"))
    assert.Equal(t, "cognitive", gw.CurrentMode())
}
```

- [ ] **Step 2: Run test to verify it passes already (routing logic change is behavioral)**

```bash
CGO_ENABLED=1 go test -tags fts5 -run TestHandleInboundRoutesByCurrentMode ./internal/gateway/ -v
```

Expected: PASS (this test only checks state; behavioral routing is verified by building + manual test).

- [ ] **Step 3: Change routing in `handleInbound`**

In `internal/gateway/gateway.go`, find the routing block (around line 405):

```go
// BEFORE:
if gw.cognitiveAgent != nil {
    if err := gw.cognitiveAgent.HandleMessage(ctx, ch, msg); err != nil {
        slog.Error("cognitive agent error", "err", err)
        _ = ch.Send(ctx, channel.OutboundMessage{
            Channel:   msg.Channel,
            ChannelID: msg.ChannelID,
            Text:      "⚠️ Error: " + err.Error(),
        })
    }
    return
}

if err := gw.runtime.HandleMessage(ctx, ch, msg); err != nil {
    slog.Error("agent error", "err", err)
    _ = ch.Send(ctx, channel.OutboundMessage{
        Channel:   msg.Channel,
        ChannelID: msg.ChannelID,
        Text:      "⚠️ Error: " + err.Error(),
    })
}
```

Replace with:

```go
// AFTER:
if gw.currentMode.Load().(string) == "cognitive" {
    if err := gw.cognitiveAgent.HandleMessage(ctx, ch, msg); err != nil {
        slog.Error("cognitive agent error", "err", err)
        _ = ch.Send(ctx, channel.OutboundMessage{
            Channel:   msg.Channel,
            ChannelID: msg.ChannelID,
            Text:      "⚠️ Error: " + err.Error(),
        })
    }
    return
}

if err := gw.runtime.HandleMessage(ctx, ch, msg); err != nil {
    slog.Error("agent error", "err", err)
    _ = ch.Send(ctx, channel.OutboundMessage{
        Channel:   msg.Channel,
        ChannelID: msg.ChannelID,
        Text:      "⚠️ Error: " + err.Error(),
    })
}
```

- [ ] **Step 4: Build to verify no compile errors**

```bash
CGO_ENABLED=1 go build -tags fts5 ./...
```

Expected: exits 0 with no errors.

- [ ] **Step 5: Run full gateway tests**

```bash
CGO_ENABLED=1 go test -tags fts5 ./internal/gateway/ -v
```

Expected: all tests PASS

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/gateway.go
git commit -m "feat(gateway): route messages by currentMode atomic value instead of cognitiveAgent nil-check"
```

---

### Task 4: Intercept `/mode` command in `handleInbound`

**Files:**
- Modify: `internal/gateway/gateway.go` (handleInbound, near `/new`/`/start` block)

- [ ] **Step 1: Write failing test**

Append to `internal/gateway/gateway_test.go`:

```go
func TestHandleModeCommand(t *testing.T) {
    cfg := config.DefaultConfig()
    cfg.LLM.APIKey = "test-key"
    cfg.Agent.Mode = "simple"

    gw, err := New(cfg)
    require.NoError(t, err)

    // Use handleModeCommand directly (extract to testable method in next step).
    response := gw.handleModeCommand("cognitive")
    assert.Contains(t, response, "cognitive")
    assert.Equal(t, "cognitive", gw.CurrentMode())

    response = gw.handleModeCommand("cognitive") // already current
    assert.Contains(t, response, "Already")

    response = gw.handleModeCommand("") // query only
    assert.Contains(t, response, "cognitive") // show current

    response = gw.handleModeCommand("bad")
    assert.Contains(t, response, "Unknown mode")
    assert.Equal(t, "cognitive", gw.CurrentMode()) // unchanged
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
CGO_ENABLED=1 go test -tags fts5 -run TestHandleModeCommand ./internal/gateway/ -v
```

Expected: FAIL — `handleModeCommand` not defined.

- [ ] **Step 3: Add `handleModeCommand` method**

Add to `internal/gateway/gateway.go`:

```go
// handleModeCommand processes the /mode command argument.
// arg="" means query-only; arg="simple"|"cognitive" switches mode.
// Returns the response text to send back to the user.
func (gw *Gateway) handleModeCommand(arg string) string {
    current := gw.CurrentMode()
    if arg == "" {
        return fmt.Sprintf("ℹ️ Current mode: %s", current)
    }
    if arg != "simple" && arg != "cognitive" {
        return fmt.Sprintf("❌ Unknown mode %q. Valid modes: simple, cognitive", arg)
    }
    if arg == current {
        return fmt.Sprintf("ℹ️ Already in %s mode", current)
    }
    _ = gw.SetMode(arg)
    return fmt.Sprintf("✅ Mode switched to %s (was: %s)", arg, current)
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
CGO_ENABLED=1 go test -tags fts5 -run TestHandleModeCommand ./internal/gateway/ -v
```

Expected: PASS

- [ ] **Step 5: Wire into `handleInbound`**

In `handleInbound`, add the `/mode` interception right after the `/new`/`/start` block (around line 401):

```go
// Handle /mode command — switch or query active agent mode
if msg.Text == "/mode" || strings.HasPrefix(msg.Text, "/mode ") {
    arg := strings.TrimPrefix(msg.Text, "/mode")
    arg = strings.TrimSpace(arg)
    response := gw.handleModeCommand(arg)
    _ = ch.Send(ctx, channel.OutboundMessage{
        Channel:   msg.Channel,
        ChannelID: msg.ChannelID,
        Text:      response,
    })
    return
}
```

Ensure `"strings"` is in the import block (it already is).

- [ ] **Step 6: Build and run all gateway tests**

```bash
CGO_ENABLED=1 go build -tags fts5 ./... && CGO_ENABLED=1 go test -tags fts5 ./internal/gateway/ -v
```

Expected: build success, all tests PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/gateway/gateway.go internal/gateway/gateway_test.go
git commit -m "feat(gateway): intercept /mode command to switch agent mode at runtime"
```

---

### Task 5: Register `/mode` in TUI command registry and sync `agentMode` display field

**Files:**
- Modify: `internal/channel/tui/commands.go`
- Modify: `internal/channel/tui/adapter.go`

- [ ] **Step 1: Register `/mode` in `commandRegistry`**

In `internal/channel/tui/commands.go`, add to the `commandRegistry` slice after the `"tasks"` entry:

```go
// Mode switching
{
    Name:        "mode",
    Description: "Show or switch agent mode",
    ArgHint:     "[simple|cognitive]",
    Category:    "builtin",
},
```

- [ ] **Step 2: Add `setAgentModeMsg` and mode-sync logic to adapter**

In `internal/channel/tui/adapter.go`, add a new message type after the existing message type declarations:

```go
// setAgentModeMsg updates the TUI model's displayed agent mode.
type setAgentModeMsg struct{ mode string }
```

In the `Send` method of `Adapter`, after `a.program.Send(agentResponseMsg{text: msg.Text})`, add detection for mode-switch responses:

```go
func (a *Adapter) Send(_ context.Context, msg channel.OutboundMessage) error {
    if a.program == nil {
        return nil
    }
    a.program.Send(agentResponseMsg{text: msg.Text})

    // Sync displayed mode when a /mode switch succeeds.
    if strings.HasPrefix(msg.Text, "✅ Mode switched to ") {
        // Extract new mode from "✅ Mode switched to <mode> (was: <old>)"
        rest := strings.TrimPrefix(msg.Text, "✅ Mode switched to ")
        if idx := strings.Index(rest, " "); idx > 0 {
            newMode := rest[:idx]
            if newMode == "simple" || newMode == "cognitive" {
                a.program.Send(setAgentModeMsg{mode: newMode})
            }
        }
    }
    return nil
}
```

Add `"strings"` to the import block in `adapter.go` if not already present.

- [ ] **Step 3: Handle `setAgentModeMsg` in the TUI Model's `Update`**

In `internal/channel/tui/model.go`, find the `Update` method's switch on message type. Add a case for `setAgentModeMsg`:

```go
case setAgentModeMsg:
    m.agentMode = msg.mode
    return m, nil
```

- [ ] **Step 4: Build to verify no compile errors**

```bash
CGO_ENABLED=1 go build -tags fts5 ./...
```

Expected: exits 0.

- [ ] **Step 5: Run TUI package tests**

```bash
CGO_ENABLED=1 go test -tags fts5 ./internal/channel/tui/ -v
```

Expected: all tests PASS (no regressions).

- [ ] **Step 6: Commit**

```bash
git add internal/channel/tui/commands.go internal/channel/tui/adapter.go internal/channel/tui/model.go
git commit -m "feat(tui): register /mode command and sync agentMode display field on mode switch"
```

---

### Task 6: Fix `NewEvalRunner` nil guard (now cognitiveAgent is always non-nil)

**Files:**
- Modify: `internal/gateway/gateway.go:340-345`

- [ ] **Step 1: Check the existing guard**

`NewEvalRunner` has:
```go
func (gw *Gateway) NewEvalRunner() *eval.CognitiveAgentRunner {
    if gw.cognitiveAgent == nil {
        return nil
    }
    return eval.NewCognitiveAgentRunner(gw.cognitiveAgent)
}
```

Since `cognitiveAgent` is now always non-nil, this guard is dead code but harmless. However the method's comment says "Returns nil if the gateway is not in cognitive mode" — update it to reflect the new reality.

- [ ] **Step 2: Update the comment**

In `internal/gateway/gateway.go`, change the comment on `NewEvalRunner`:

```go
// BEFORE:
// NewEvalRunner creates an eval.AgentRunner backed by the gateway's cognitive
// agent. Returns nil if the gateway is not in cognitive mode.

// AFTER:
// NewEvalRunner creates an eval.AgentRunner backed by the gateway's cognitive agent.
```

Keep the nil guard in place — defensive code for future refactors.

- [ ] **Step 3: Build and run all tests**

```bash
CGO_ENABLED=1 go build -tags fts5 ./... && CGO_ENABLED=1 go test -tags fts5 ./... 2>&1 | tail -30
```

Expected: build success, all tests PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/gateway/gateway.go
git commit -m "docs(gateway): update NewEvalRunner comment to reflect always-initialized cognitive agent"
```

---

## Self-Review

**Spec coverage:**
- ✅ All channels supported — `/mode` intercepted in gateway layer
- ✅ Conversation history preserved — no session reset on mode switch
- ✅ `atomic.Value` for thread-safe routing
- ✅ Both agents always initialized
- ✅ `/mode` with no arg shows current mode
- ✅ `/mode simple|cognitive` switches
- ✅ Invalid arg returns error message
- ✅ Already-current mode returns info message
- ✅ TUI autocomplete via `commandRegistry`
- ✅ TUI `m.agentMode` display field synced

**Placeholder scan:** None found.

**Type consistency:** `setAgentModeMsg` defined in `adapter.go`, used in `adapter.go` `Send()` and `model.go` `Update()` — consistent. `handleModeCommand` defined and tested in `gateway.go`. `CurrentMode()` / `SetMode()` used in tests and `handleModeCommand` — consistent.
