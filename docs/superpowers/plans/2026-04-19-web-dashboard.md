# Web Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an embedded Web Dashboard to IronClaw with real-time Agent monitoring via WebSocket, serving a Preact SPA from the Go binary.

**Architecture:** In-process Event Bus collects agent lifecycle events (phase transitions, tool calls). A WebSocket Hub broadcasts events to browser clients. REST API provides historical data from SQLite. Preact SPA (go:embed) renders an Overview page with live agent status, phase timeline, and tool call feed.

**Tech Stack:** Go stdlib `net/http`, `gorilla/websocket`, `go:embed`; Preact, Vite, wouter, TypeScript

**Spec:** `docs/superpowers/specs/2026-04-19-web-dashboard-design.md`

---

## File Structure

### New files (Go backend)

| File | Responsibility |
|------|---------------|
| `internal/dashboard/eventbus.go` | In-process pub/sub event bus |
| `internal/dashboard/eventbus_test.go` | Event bus tests |
| `internal/dashboard/emitter.go` | `DashboardEmitter` implementation (publishes to bus) |
| `internal/dashboard/emitter_test.go` | Emitter tests |
| `internal/dashboard/state_tracker.go` | Subscribes to bus, maintains agent real-time state |
| `internal/dashboard/state_tracker_test.go` | State tracker tests |
| `internal/dashboard/evolution_bridge.go` | `evolution.Hook` adapter → bus |
| `internal/dashboard/evolution_bridge_test.go` | Bridge tests |
| `internal/dashboard/ws_hub.go` | WebSocket hub, broadcasts events to browser clients |
| `internal/dashboard/ws_hub_test.go` | Hub tests |
| `internal/dashboard/server.go` | HTTP server: REST API + WS upgrade + SPA serving |
| `internal/dashboard/server_test.go` | Server endpoint tests |
| `internal/dashboard/embed.go` | `go:embed` directive for `web/dist/` |
| `internal/agent/dashboard_emitter.go` | `DashboardEmitter` interface definition |
| `internal/gateway/init_dashboard.go` | Gateway wiring for dashboard subsystem |

### New files (Frontend)

| File | Responsibility |
|------|---------------|
| `web/package.json` | NPM dependencies |
| `web/vite.config.ts` | Vite config with Preact and API proxy |
| `web/tsconfig.json` | TypeScript config |
| `web/index.html` | SPA entry HTML |
| `web/src/main.tsx` | Preact mount point |
| `web/src/app.tsx` | Root component + router |
| `web/src/lib/types.ts` | Shared TypeScript types |
| `web/src/lib/api.ts` | REST API client |
| `web/src/hooks/useWebSocket.ts` | WS connection + auto-reconnect |
| `web/src/hooks/useAgentState.ts` | Agent state reducer |
| `web/src/components/Layout.tsx` | Page layout shell |
| `web/src/components/AgentStatus.tsx` | Status card |
| `web/src/components/PhaseTimeline.tsx` | Phase flow visualization |
| `web/src/components/ToolCallFeed.tsx` | Scrolling tool call list |
| `web/src/components/SessionList.tsx` | Active session list |
| `web/src/pages/Overview.tsx` | Main dashboard page |
| `web/src/pages/NotFound.tsx` | 404 page |
| `web/src/styles/global.css` | Global styles + dark theme |

### Modified files

| File | Change |
|------|--------|
| `internal/config/config.go` | Add `DashboardConfig` struct, add `Dashboard` field to `Config` |
| `internal/agent/cognitive.go` | Add `dashEmitter` field, emit events around `registerSubtask` calls |
| `internal/agent/runtime.go` | Add `dashEmitter` field + `SetDashboardEmitter` method |
| `internal/agent/act.go` | Emit tool start/end events around `t.Execute` |
| `internal/gateway/gateway.go` | Add dashboard fields, call `initDashboard` in `New()`, update `Start()`/`Stop()` |
| `internal/gateway/http.go` | Delete file (functionality moves to `dashboard/server.go`) |
| `configs/ironclaw.example.yaml` | Replace `server:` with `dashboard:` section |
| `Makefile` | Add `web` build target, make `build` depend on `web` |
| `go.mod` / `go.sum` | Add `github.com/gorilla/websocket` |

---

### Task 1: Event Bus

**Files:**
- Create: `internal/dashboard/eventbus.go`
- Test: `internal/dashboard/eventbus_test.go`

- [ ] **Step 1: Write failing tests for Event Bus**

```go
// internal/dashboard/eventbus_test.go
package dashboard

import (
	"testing"
	"time"
)

func TestBusPublishToSubscriber(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	bus.Publish(Event{
		Type:      EventPhaseStart,
		Timestamp: time.Now(),
		SessionID: "s1",
		Data:      map[string]any{"phase": "PLAN"},
	})

	select {
	case ev := <-ch:
		if ev.Type != EventPhaseStart {
			t.Fatalf("got type %s, want phase.start", ev.Type)
		}
		if ev.Data["phase"] != "PLAN" {
			t.Fatalf("got phase %v, want PLAN", ev.Data["phase"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestBusMultipleSubscribers(t *testing.T) {
	bus := NewBus(16)
	ch1 := bus.Subscribe()
	ch2 := bus.Subscribe()
	defer bus.Unsubscribe(ch1)
	defer bus.Unsubscribe(ch2)

	bus.Publish(Event{Type: EventToolStart, Timestamp: time.Now()})

	for _, ch := range []chan Event{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.Type != EventToolStart {
				t.Fatalf("got %s, want tool.start", ev.Type)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive event")
		}
	}
}

func TestBusUnsubscribe(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	bus.Unsubscribe(ch)

	bus.Publish(Event{Type: EventAgentIdle, Timestamp: time.Now()})

	select {
	case <-ch:
		t.Fatal("unsubscribed channel should not receive events")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestBusSlowSubscriberDoesNotBlock(t *testing.T) {
	bus := NewBus(1) // buffer of 1
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	// Fill buffer
	bus.Publish(Event{Type: EventPhaseStart, Timestamp: time.Now()})
	// This should not block even though buffer is full
	bus.Publish(Event{Type: EventPhaseEnd, Timestamp: time.Now()})

	ev := <-ch
	if ev.Type != EventPhaseStart {
		t.Fatalf("got %s, want phase.start", ev.Type)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags fts5 -run TestBus ./internal/dashboard/ -v`
Expected: FAIL — package does not exist

- [ ] **Step 3: Implement Event Bus**

```go
// internal/dashboard/eventbus.go
package dashboard

import (
	"sync"
	"time"
)

type EventType string

const (
	EventPhaseStart    EventType = "phase.start"
	EventPhaseEnd      EventType = "phase.end"
	EventToolStart     EventType = "tool.start"
	EventToolEnd       EventType = "tool.end"
	EventPlanGenerated EventType = "plan.generated"
	EventReplanStart   EventType = "replan.start"
	EventTaskUpdate    EventType = "task.update"
	EventSessionStart  EventType = "session.start"
	EventSessionEnd    EventType = "session.end"
	EventAgentIdle     EventType = "agent.idle"
)

type Event struct {
	Type      EventType      `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	SessionID string         `json:"session_id,omitempty"`
	Data      map[string]any `json:"data"`
}

type Bus struct {
	subscribers map[chan Event]struct{}
	mu          sync.RWMutex
	bufSize     int
}

func NewBus(bufSize int) *Bus {
	if bufSize <= 0 {
		bufSize = 256
	}
	return &Bus{
		subscribers: make(map[chan Event]struct{}),
		bufSize:     bufSize,
	}
}

func (b *Bus) Subscribe() chan Event {
	ch := make(chan Event, b.bufSize)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *Bus) Unsubscribe(ch chan Event) {
	b.mu.Lock()
	delete(b.subscribers, ch)
	b.mu.Unlock()
}

func (b *Bus) Publish(event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subscribers {
		select {
		case ch <- event:
		default:
			// slow subscriber — drop event to avoid blocking
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags fts5 -run TestBus ./internal/dashboard/ -v`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/dashboard/eventbus.go internal/dashboard/eventbus_test.go
git commit -m "feat(dashboard): add in-process event bus"
```

---

### Task 2: DashboardEmitter Interface

**Files:**
- Create: `internal/agent/dashboard_emitter.go`

- [ ] **Step 1: Create the interface file**

```go
// internal/agent/dashboard_emitter.go
package agent

// DashboardEmitter emits agent lifecycle events for the web dashboard.
// Implementations must be safe for concurrent use. All methods are no-ops
// when the receiver is nil, so callers need not nil-check.
type DashboardEmitter interface {
	EmitPhaseStart(sessionID, phase string)
	EmitPhaseEnd(sessionID, phase string, durationMs int64)
	EmitToolStart(sessionID, toolName, input string)
	EmitToolEnd(sessionID, toolName string, succeeded bool, durationMs int64)
}
```

- [ ] **Step 2: Verify compilation**

Run: `CGO_ENABLED=1 go build -tags fts5 ./internal/agent/`
Expected: success

- [ ] **Step 3: Add field and setter to Runtime**

In `internal/agent/runtime.go`, add `dashEmitter` field to the `Runtime` struct (after line ~57, near `taskLedger`):

```go
// Add to Runtime struct
dashEmitter DashboardEmitter
```

Add setter method at the end of the file:

```go
func (r *Runtime) SetDashboardEmitter(e DashboardEmitter) {
	r.dashEmitter = e
}
```

- [ ] **Step 4: Add field and setter to CognitiveAgent**

In `internal/agent/cognitive.go`, add `dashEmitter` field to `CognitiveAgent` struct:

```go
// Add to CognitiveAgent struct
dashEmitter DashboardEmitter
```

Add setter method:

```go
func (ca *CognitiveAgent) SetDashboardEmitter(e DashboardEmitter) {
	ca.dashEmitter = e
}
```

- [ ] **Step 5: Verify compilation**

Run: `CGO_ENABLED=1 go build -tags fts5 ./internal/agent/`
Expected: success

- [ ] **Step 6: Commit**

```bash
git add internal/agent/dashboard_emitter.go internal/agent/runtime.go internal/agent/cognitive.go
git commit -m "feat(agent): add DashboardEmitter interface and fields"
```

---

### Task 3: Dashboard Emitter Implementation

**Files:**
- Create: `internal/dashboard/emitter.go`
- Test: `internal/dashboard/emitter_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/dashboard/emitter_test.go
package dashboard

import (
	"testing"
	"time"
)

func TestEmitterPhaseStart(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	em := NewEmitter(bus)
	em.EmitPhaseStart("s1", "PLAN")

	select {
	case ev := <-ch:
		if ev.Type != EventPhaseStart {
			t.Fatalf("type = %s, want phase.start", ev.Type)
		}
		if ev.SessionID != "s1" {
			t.Fatalf("session = %s, want s1", ev.SessionID)
		}
		if ev.Data["phase"] != "PLAN" {
			t.Fatalf("phase = %v, want PLAN", ev.Data["phase"])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestEmitterToolEndWithDuration(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	em := NewEmitter(bus)
	em.EmitToolEnd("s1", "bash", true, 250)

	select {
	case ev := <-ch:
		if ev.Type != EventToolEnd {
			t.Fatalf("type = %s, want tool.end", ev.Type)
		}
		if ev.Data["tool_name"] != "bash" {
			t.Fatalf("tool = %v, want bash", ev.Data["tool_name"])
		}
		if ev.Data["succeeded"] != true {
			t.Fatalf("succeeded = %v, want true", ev.Data["succeeded"])
		}
		if ev.Data["duration_ms"] != int64(250) {
			t.Fatalf("duration = %v, want 250", ev.Data["duration_ms"])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestEmitterTruncatesLongInput(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	em := NewEmitter(bus)
	longInput := make([]byte, 1000)
	for i := range longInput {
		longInput[i] = 'a'
	}
	em.EmitToolStart("s1", "bash", string(longInput))

	select {
	case ev := <-ch:
		input := ev.Data["input"].(string)
		if len(input) > 503 { // 500 + "..."
			t.Fatalf("input length = %d, want <= 503", len(input))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags fts5 -run TestEmitter ./internal/dashboard/ -v`
Expected: FAIL — `NewEmitter` undefined

- [ ] **Step 3: Implement Emitter**

```go
// internal/dashboard/emitter.go
package dashboard

import "time"

const maxInputLen = 500

type Emitter struct {
	bus *Bus
}

func NewEmitter(bus *Bus) *Emitter {
	return &Emitter{bus: bus}
}

func (e *Emitter) EmitPhaseStart(sessionID, phase string) {
	e.bus.Publish(Event{
		Type:      EventPhaseStart,
		Timestamp: time.Now(),
		SessionID: sessionID,
		Data:      map[string]any{"phase": phase},
	})
}

func (e *Emitter) EmitPhaseEnd(sessionID, phase string, durationMs int64) {
	e.bus.Publish(Event{
		Type:      EventPhaseEnd,
		Timestamp: time.Now(),
		SessionID: sessionID,
		Data:      map[string]any{"phase": phase, "duration_ms": durationMs},
	})
}

func (e *Emitter) EmitToolStart(sessionID, toolName, input string) {
	if len(input) > maxInputLen {
		input = input[:maxInputLen] + "..."
	}
	e.bus.Publish(Event{
		Type:      EventToolStart,
		Timestamp: time.Now(),
		SessionID: sessionID,
		Data:      map[string]any{"tool_name": toolName, "input": input},
	})
}

func (e *Emitter) EmitToolEnd(sessionID, toolName string, succeeded bool, durationMs int64) {
	e.bus.Publish(Event{
		Type:      EventToolEnd,
		Timestamp: time.Now(),
		SessionID: sessionID,
		Data: map[string]any{
			"tool_name":   toolName,
			"succeeded":   succeeded,
			"duration_ms": durationMs,
		},
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags fts5 -run TestEmitter ./internal/dashboard/ -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/dashboard/emitter.go internal/dashboard/emitter_test.go
git commit -m "feat(dashboard): add emitter that publishes agent events to bus"
```

---

### Task 4: Agent State Tracker

**Files:**
- Create: `internal/dashboard/state_tracker.go`
- Test: `internal/dashboard/state_tracker_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/dashboard/state_tracker_test.go
package dashboard

import (
	"testing"
	"time"
)

func TestStateTrackerPhaseTransition(t *testing.T) {
	bus := NewBus(16)
	tracker := NewAgentStateTracker(bus)
	go tracker.Run()
	defer tracker.Stop()

	bus.Publish(Event{
		Type: EventPhaseStart, Timestamp: time.Now(), SessionID: "s1",
		Data: map[string]any{"phase": "PLAN"},
	})
	time.Sleep(50 * time.Millisecond)

	snap := tracker.Snapshot()
	if snap.Status != "busy" {
		t.Fatalf("status = %s, want busy", snap.Status)
	}
	if len(snap.ActiveSessions) != 1 {
		t.Fatalf("active sessions = %d, want 1", len(snap.ActiveSessions))
	}
	if snap.ActiveSessions[0].CurrentPhase != "PLAN" {
		t.Fatalf("phase = %s, want PLAN", snap.ActiveSessions[0].CurrentPhase)
	}
}

func TestStateTrackerToolExecution(t *testing.T) {
	bus := NewBus(16)
	tracker := NewAgentStateTracker(bus)
	go tracker.Run()
	defer tracker.Stop()

	bus.Publish(Event{
		Type: EventToolStart, Timestamp: time.Now(), SessionID: "s1",
		Data: map[string]any{"tool_name": "bash"},
	})
	time.Sleep(50 * time.Millisecond)

	snap := tracker.Snapshot()
	if len(snap.ActiveSessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(snap.ActiveSessions))
	}
	if snap.ActiveSessions[0].CurrentTool != "bash" {
		t.Fatalf("tool = %s, want bash", snap.ActiveSessions[0].CurrentTool)
	}

	bus.Publish(Event{
		Type: EventToolEnd, Timestamp: time.Now(), SessionID: "s1",
		Data: map[string]any{"tool_name": "bash", "succeeded": true},
	})
	time.Sleep(50 * time.Millisecond)

	snap = tracker.Snapshot()
	if snap.ActiveSessions[0].CurrentTool != "" {
		t.Fatalf("tool should be cleared, got %s", snap.ActiveSessions[0].CurrentTool)
	}
	if snap.ActiveSessions[0].ToolsExecuted != 1 {
		t.Fatalf("tools_executed = %d, want 1", snap.ActiveSessions[0].ToolsExecuted)
	}
}

func TestStateTrackerSessionEnd(t *testing.T) {
	bus := NewBus(16)
	tracker := NewAgentStateTracker(bus)
	go tracker.Run()
	defer tracker.Stop()

	bus.Publish(Event{
		Type: EventPhaseStart, Timestamp: time.Now(), SessionID: "s1",
		Data: map[string]any{"phase": "PERCEIVE"},
	})
	time.Sleep(50 * time.Millisecond)

	bus.Publish(Event{
		Type: EventSessionEnd, Timestamp: time.Now(), SessionID: "s1",
		Data: map[string]any{},
	})
	time.Sleep(50 * time.Millisecond)

	snap := tracker.Snapshot()
	if snap.Status != "idle" {
		t.Fatalf("status = %s, want idle", snap.Status)
	}
	if len(snap.ActiveSessions) != 0 {
		t.Fatalf("sessions = %d, want 0", len(snap.ActiveSessions))
	}
	if snap.TotalSessionsToday != 1 {
		t.Fatalf("total = %d, want 1", snap.TotalSessionsToday)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags fts5 -run TestStateTracker ./internal/dashboard/ -v`
Expected: FAIL — `NewAgentStateTracker` undefined

- [ ] **Step 3: Implement State Tracker**

```go
// internal/dashboard/state_tracker.go
package dashboard

import (
	"sync"
	"time"
)

type SessionState struct {
	SessionID    string    `json:"session_id"`
	Channel      string    `json:"channel,omitempty"`
	CurrentPhase string    `json:"current_phase"`
	CurrentTool  string    `json:"current_tool,omitempty"`
	PhaseStart   time.Time `json:"phase_started_at,omitempty"`
	ToolsExecuted int      `json:"tools_executed"`
	ReplanCount   int      `json:"replan_count"`
}

type StateSnapshot struct {
	Status            string          `json:"status"`
	ActiveSessions    []SessionState  `json:"active_sessions"`
	UptimeSeconds     int64           `json:"uptime_seconds"`
	TotalSessionsToday int            `json:"total_sessions_today"`
}

type AgentStateTracker struct {
	bus            *Bus
	eventCh        chan Event
	mu             sync.RWMutex
	activeSessions map[string]*SessionState
	totalToday     int
	startedAt      time.Time
	stopCh         chan struct{}
}

func NewAgentStateTracker(bus *Bus) *AgentStateTracker {
	return &AgentStateTracker{
		bus:            bus,
		eventCh:        bus.Subscribe(),
		activeSessions: make(map[string]*SessionState),
		startedAt:      time.Now(),
		stopCh:         make(chan struct{}),
	}
}

func (t *AgentStateTracker) Run() {
	for {
		select {
		case ev := <-t.eventCh:
			t.handleEvent(ev)
		case <-t.stopCh:
			t.bus.Unsubscribe(t.eventCh)
			return
		}
	}
}

func (t *AgentStateTracker) Stop() {
	select {
	case <-t.stopCh:
	default:
		close(t.stopCh)
	}
}

func (t *AgentStateTracker) handleEvent(ev Event) {
	t.mu.Lock()
	defer t.mu.Unlock()

	sid := ev.SessionID
	if sid == "" {
		return
	}

	switch ev.Type {
	case EventPhaseStart:
		ss := t.getOrCreate(sid)
		if phase, ok := ev.Data["phase"].(string); ok {
			ss.CurrentPhase = phase
		}
		ss.PhaseStart = ev.Timestamp

	case EventPhaseEnd:
		ss := t.getOrCreate(sid)
		ss.CurrentPhase = ""

	case EventToolStart:
		ss := t.getOrCreate(sid)
		if name, ok := ev.Data["tool_name"].(string); ok {
			ss.CurrentTool = name
		}

	case EventToolEnd:
		ss := t.getOrCreate(sid)
		ss.CurrentTool = ""
		ss.ToolsExecuted++

	case EventReplanStart:
		ss := t.getOrCreate(sid)
		ss.ReplanCount++

	case EventSessionEnd:
		delete(t.activeSessions, sid)
		t.totalToday++
	}
}

func (t *AgentStateTracker) getOrCreate(sessionID string) *SessionState {
	ss, ok := t.activeSessions[sessionID]
	if !ok {
		ss = &SessionState{SessionID: sessionID}
		t.activeSessions[sessionID] = ss
	}
	return ss
}

func (t *AgentStateTracker) Snapshot() StateSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()

	sessions := make([]SessionState, 0, len(t.activeSessions))
	for _, ss := range t.activeSessions {
		sessions = append(sessions, *ss)
	}

	status := "idle"
	if len(sessions) > 0 {
		status = "busy"
	}

	return StateSnapshot{
		Status:             status,
		ActiveSessions:     sessions,
		UptimeSeconds:      int64(time.Since(t.startedAt).Seconds()),
		TotalSessionsToday: t.totalToday + len(t.activeSessions),
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags fts5 -run TestStateTracker ./internal/dashboard/ -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/dashboard/state_tracker.go internal/dashboard/state_tracker_test.go
git commit -m "feat(dashboard): add agent state tracker"
```

---

### Task 5: Evolution Bridge

**Files:**
- Create: `internal/dashboard/evolution_bridge.go`
- Test: `internal/dashboard/evolution_bridge_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/dashboard/evolution_bridge_test.go
package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/evolution"
)

func TestEvolutionBridgeToolExec(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	bridge := NewEvolutionBridge(bus)

	bridge.OnToolExecuted(context.Background(), evolution.ToolExecEvent{
		SessionID:  "s1",
		ToolName:   "bash",
		Succeeded:  true,
		DurationMs: 100,
		Timestamp:  time.Now(),
	})

	select {
	case ev := <-ch:
		if ev.Type != EventToolEnd {
			t.Fatalf("type = %s, want tool.end", ev.Type)
		}
		if ev.Data["tool_name"] != "bash" {
			t.Fatalf("tool = %v, want bash", ev.Data["tool_name"])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestEvolutionBridgeName(t *testing.T) {
	bridge := NewEvolutionBridge(NewBus(1))
	if bridge.Name() != "dashboard_bridge" {
		t.Fatalf("name = %s, want dashboard_bridge", bridge.Name())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags fts5 -run TestEvolutionBridge ./internal/dashboard/ -v`
Expected: FAIL — `NewEvolutionBridge` undefined

- [ ] **Step 3: Implement Evolution Bridge**

```go
// internal/dashboard/evolution_bridge.go
package dashboard

import (
	"context"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/evolution"
)

// EvolutionBridge implements evolution.Hook and forwards events to the dashboard Bus.
type EvolutionBridge struct {
	bus *Bus
}

func NewEvolutionBridge(bus *Bus) *EvolutionBridge {
	return &EvolutionBridge{bus: bus}
}

func (b *EvolutionBridge) Name() string { return "dashboard_bridge" }

func (b *EvolutionBridge) OnReflectionComplete(_ context.Context, event evolution.ReflectionEvent) {
	b.bus.Publish(Event{
		Type:      EventPhaseEnd,
		Timestamp: event.Timestamp,
		SessionID: event.SessionID,
		Data: map[string]any{
			"phase":      "REFLECT",
			"succeeded":  event.Succeeded,
			"confidence": event.Confidence,
		},
	})
}

func (b *EvolutionBridge) OnEpisodeComplete(_ context.Context, event evolution.EpisodeEvent) {
	b.bus.Publish(Event{
		Type:      EventSessionEnd,
		Timestamp: event.Timestamp,
		SessionID: event.SessionID,
		Data: map[string]any{
			"succeeded":    event.Succeeded,
			"duration_ms":  event.DurationMs,
			"replan_count": event.ReplanCount,
		},
	})
}

func (b *EvolutionBridge) OnToolExecuted(_ context.Context, event evolution.ToolExecEvent) {
	b.bus.Publish(Event{
		Type:      EventToolEnd,
		Timestamp: event.Timestamp,
		SessionID: event.SessionID,
		Data: map[string]any{
			"tool_name":   event.ToolName,
			"succeeded":   event.Succeeded,
			"duration_ms": event.DurationMs,
		},
	})
}

// compile-time check
var _ evolution.Hook = (*EvolutionBridge)(nil)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags fts5 -run TestEvolutionBridge ./internal/dashboard/ -v`
Expected: PASS (2 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/dashboard/evolution_bridge.go internal/dashboard/evolution_bridge_test.go
git commit -m "feat(dashboard): add evolution hook bridge"
```

---

### Task 6: WebSocket Hub

**Files:**
- Create: `internal/dashboard/ws_hub.go`
- Test: `internal/dashboard/ws_hub_test.go`

- [ ] **Step 1: Add gorilla/websocket dependency**

Run: `go get github.com/gorilla/websocket`

- [ ] **Step 2: Write failing tests**

```go
// internal/dashboard/ws_hub_test.go
package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestHubBroadcastsEvents(t *testing.T) {
	bus := NewBus(16)
	hub := NewHub(bus)
	go hub.Run()
	defer hub.Stop()

	server := httptest.NewServer(http.HandlerFunc(hub.HandleWS))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	bus.Publish(Event{
		Type:      EventPhaseStart,
		Timestamp: time.Now(),
		SessionID: "s1",
		Data:      map[string]any{"phase": "ACT"},
	})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var ev Event
	if err := conn.ReadJSON(&ev); err != nil {
		t.Fatalf("read: %v", err)
	}
	if ev.Type != EventPhaseStart {
		t.Fatalf("type = %s, want phase.start", ev.Type)
	}
}

func TestHubClientDisconnect(t *testing.T) {
	bus := NewBus(16)
	hub := NewHub(bus)
	go hub.Run()
	defer hub.Stop()

	server := httptest.NewServer(http.HandlerFunc(hub.HandleWS))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if hub.ClientCount() != 1 {
		t.Fatalf("clients = %d, want 1", hub.ClientCount())
	}

	conn.Close()
	time.Sleep(100 * time.Millisecond)
	if hub.ClientCount() != 0 {
		t.Fatalf("clients = %d after disconnect, want 0", hub.ClientCount())
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags fts5 -run TestHub ./internal/dashboard/ -v`
Expected: FAIL — `NewHub` undefined

- [ ] **Step 4: Implement WebSocket Hub**

```go
// internal/dashboard/ws_hub.go
package dashboard

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type client struct {
	conn *websocket.Conn
	send chan []byte
}

type Hub struct {
	bus     *Bus
	eventCh chan Event

	clients    map[*client]struct{}
	register   chan *client
	unregister chan *client
	mu         sync.RWMutex
	stopCh     chan struct{}
}

func NewHub(bus *Bus) *Hub {
	return &Hub{
		bus:        bus,
		eventCh:    bus.Subscribe(),
		clients:    make(map[*client]struct{}),
		register:   make(chan *client),
		unregister: make(chan *client),
		stopCh:     make(chan struct{}),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = struct{}{}
			h.mu.Unlock()

		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			h.mu.Unlock()

		case ev := <-h.eventCh:
			data, err := json.Marshal(ev)
			if err != nil {
				slog.Warn("dashboard: failed to marshal event", "err", err)
				continue
			}
			h.mu.RLock()
			for c := range h.clients {
				select {
				case c.send <- data:
				default:
					// slow client
				}
			}
			h.mu.RUnlock()

		case <-h.stopCh:
			h.bus.Unsubscribe(h.eventCh)
			h.mu.Lock()
			for c := range h.clients {
				close(c.send)
				delete(h.clients, c)
			}
			h.mu.Unlock()
			return
		}
	}
}

func (h *Hub) Stop() {
	select {
	case <-h.stopCh:
	default:
		close(h.stopCh)
	}
}

func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn("dashboard: ws upgrade failed", "err", err)
		return
	}

	c := &client{conn: conn, send: make(chan []byte, 64)}
	h.register <- c

	go h.writePump(c)
	go h.readPump(c)
}

func (h *Hub) writePump(c *client) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (h *Hub) readPump(c *client) {
	defer func() {
		h.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			break
		}
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags fts5 -run TestHub ./internal/dashboard/ -v`
Expected: PASS (2 tests)

- [ ] **Step 6: Commit**

```bash
git add internal/dashboard/ws_hub.go internal/dashboard/ws_hub_test.go go.mod go.sum
git commit -m "feat(dashboard): add WebSocket hub with broadcast"
```

---

### Task 7: Dashboard Config

**Files:**
- Modify: `internal/config/config.go`
- Modify: `configs/ironclaw.example.yaml`

- [ ] **Step 1: Add DashboardConfig struct**

In `internal/config/config.go`, add near the existing `ServerConfig` (around line 357):

```go
type DashboardConfig struct {
	Enabled bool   `yaml:"enabled"`
	Addr    string `yaml:"addr"`
	Token   string `yaml:"token"`
}
```

- [ ] **Step 2: Add Dashboard field to Config struct**

In `internal/config/config.go`, add `Dashboard DashboardConfig` to the `Config` struct (after `Server ServerConfig`):

```go
Dashboard  DashboardConfig  `yaml:"dashboard"`
```

- [ ] **Step 3: Update example config**

In `configs/ironclaw.example.yaml`, replace the `server:` section (lines 278-280):

Old:
```yaml
server:
  addr: ":8080"
  enabled: false
```

New:
```yaml
server:
  addr: ":8080"
  enabled: false

# Web Dashboard — real-time agent monitoring UI
dashboard:
  enabled: false
  addr: "127.0.0.1:8080"
  token: ""
```

Keep the `server` block for backward compatibility; dashboard is additive.

- [ ] **Step 4: Verify compilation**

Run: `CGO_ENABLED=1 go build -tags fts5 ./internal/config/`
Expected: success

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go configs/ironclaw.example.yaml
git commit -m "feat(config): add dashboard configuration"
```

---

### Task 8: HTTP Server + REST API

**Files:**
- Create: `internal/dashboard/server.go`
- Test: `internal/dashboard/server_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/dashboard/server_test.go
package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/Forest-Isle/IronClaw/internal/config"
)

func TestAgentStateEndpoint(t *testing.T) {
	bus := NewBus(16)
	tracker := NewAgentStateTracker(bus)

	deps := ServerDeps{
		Tracker:  tracker,
		StaticFS: fstest.MapFS{"index.html": {Data: []byte("<html></html>")}},
	}
	handler := NewServerMux(deps)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/agent/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var snap StateSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatal(err)
	}
	if snap.Status != "idle" {
		t.Fatalf("status = %s, want idle", snap.Status)
	}
}

func TestHealthEndpoint(t *testing.T) {
	deps := ServerDeps{
		Tracker:  NewAgentStateTracker(NewBus(1)),
		StaticFS: fstest.MapFS{"index.html": {Data: []byte("<html></html>")}},
	}
	handler := NewServerMux(deps)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var m map[string]string
	json.Unmarshal(body, &m)
	if m["status"] != "ok" {
		t.Fatalf("health = %v, want ok", m["status"])
	}
}

func TestSPAFallback(t *testing.T) {
	deps := ServerDeps{
		Tracker:  NewAgentStateTracker(NewBus(1)),
		StaticFS: fstest.MapFS{"index.html": {Data: []byte("<html>SPA</html>")}},
	}
	handler := NewServerMux(deps)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/some/route")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "<html>SPA</html>" {
		t.Fatalf("SPA fallback failed, got %s", string(body))
	}
}

func TestTokenAuth(t *testing.T) {
	deps := ServerDeps{
		Tracker:  NewAgentStateTracker(NewBus(1)),
		StaticFS: fstest.MapFS{"index.html": {Data: []byte("<html></html>")}},
		Token:    "secret123",
	}
	handler := NewServerMux(deps)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// No token → 401
	resp, _ := http.Get(srv.URL + "/api/agent/state")
	if resp.StatusCode != 401 {
		t.Fatalf("no token: status = %d, want 401", resp.StatusCode)
	}

	// With token → 200
	req, _ := http.NewRequest("GET", srv.URL+"/api/agent/state", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Fatalf("with token: status = %d, want 200", resp.StatusCode)
	}

	// Token via query param (for WebSocket) → 200
	resp, _ = http.Get(srv.URL + "/api/agent/state?token=secret123")
	if resp.StatusCode != 200 {
		t.Fatalf("query token: status = %d, want 200", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags fts5 -run "TestAgentState|TestHealth|TestSPA|TestToken" ./internal/dashboard/ -v`
Expected: FAIL — `NewServerMux` undefined

- [ ] **Step 3: Implement HTTP Server**

```go
// internal/dashboard/server.go
package dashboard

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/cogmetrics"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/store"
)

type ServerDeps struct {
	DB        *store.DB
	Hub       *Hub
	Tracker   *AgentStateTracker
	Collector *cogmetrics.Collector
	StaticFS  fs.FS
	Token     string
}

func NewServerMux(deps ServerDeps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/agent/state", deps.authMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(deps.Tracker.Snapshot())
	}))

	mux.HandleFunc("/api/sessions", deps.authMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if deps.DB == nil {
			http.Error(w, "database not available", 503)
			return
		}
		rows, err := deps.DB.Query("SELECT id, channel, channel_id, created_at, updated_at FROM sessions ORDER BY updated_at DESC LIMIT 50")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()

		type sessionInfo struct {
			ID        string `json:"id"`
			Channel   string `json:"channel"`
			ChannelID string `json:"channel_id"`
			CreatedAt string `json:"created_at"`
			UpdatedAt string `json:"updated_at"`
		}
		var sessions []sessionInfo
		for rows.Next() {
			var s sessionInfo
			if err := rows.Scan(&s.ID, &s.Channel, &s.ChannelID, &s.CreatedAt, &s.UpdatedAt); err != nil {
				continue
			}
			sessions = append(sessions, s)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sessions)
	}))

	mux.HandleFunc("/api/sessions/{id}/messages", deps.authMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if deps.DB == nil {
			http.Error(w, "database not available", 503)
			return
		}
		sessionID := r.PathValue("id")
		rows, err := deps.DB.Query(
			"SELECT id, role, content, tool_name, created_at FROM messages WHERE session_id = ? ORDER BY created_at ASC",
			sessionID,
		)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()

		type msg struct {
			ID        string  `json:"id"`
			Role      string  `json:"role"`
			Content   string  `json:"content"`
			ToolName  *string `json:"tool_name,omitempty"`
			CreatedAt string  `json:"created_at"`
		}
		var msgs []msg
		for rows.Next() {
			var m msg
			if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.ToolName, &m.CreatedAt); err != nil {
				continue
			}
			msgs = append(msgs, m)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(msgs)
	}))

	mux.HandleFunc("/api/sessions/{id}/tools", deps.authMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if deps.DB == nil {
			http.Error(w, "database not available", 503)
			return
		}
		sessionID := r.PathValue("id")
		rows, err := deps.DB.Query(
			"SELECT id, tool_name, input, output, status, duration_ms, created_at FROM tool_log WHERE session_id = ? ORDER BY created_at ASC",
			sessionID,
		)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()

		type toolEntry struct {
			ID         string `json:"id"`
			ToolName   string `json:"tool_name"`
			Input      string `json:"input"`
			Output     string `json:"output"`
			Status     string `json:"status"`
			DurationMs int64  `json:"duration_ms"`
			CreatedAt  string `json:"created_at"`
		}
		var entries []toolEntry
		for rows.Next() {
			var e toolEntry
			if err := rows.Scan(&e.ID, &e.ToolName, &e.Input, &e.Output, &e.Status, &e.DurationMs, &e.CreatedAt); err != nil {
				continue
			}
			entries = append(entries, e)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	}))

	if deps.Hub != nil {
		mux.HandleFunc("/ws", deps.authMiddleware(deps.Hub.HandleWS))
	}

	if deps.Collector != nil {
		mux.HandleFunc("/api/metrics/health", deps.authMiddleware(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(deps.Collector.Snapshot())
		}))
	}

	// SPA: serve static files, fallback unknown routes to index.html
	mux.Handle("/", spaHandler{fs: deps.StaticFS})

	return mux
}

type spaHandler struct {
	fs fs.FS
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}

	if _, err := fs.Stat(h.fs, path); err != nil {
		path = "index.html"
	}

	http.ServeFileFS(w, r, h.fs, path)
}

func (d ServerDeps) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	if d.Token == "" {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		if token != d.Token {
			http.Error(w, "unauthorized", 401)
			return
		}
		next(w, r)
	}
}

func StartServer(cfg config.DashboardConfig, deps ServerDeps) {
	deps.Token = cfg.Token
	handler := NewServerMux(deps)
	slog.Info("dashboard server starting", "addr", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		slog.Error("dashboard server error", "err", err)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags fts5 -run "TestAgentState|TestHealth|TestSPA|TestToken" ./internal/dashboard/ -v`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/dashboard/server.go internal/dashboard/server_test.go
git commit -m "feat(dashboard): add HTTP server with REST API and SPA serving"
```

---

### Task 9: Agent Integration

**Files:**
- Modify: `internal/agent/cognitive.go` (lines ~316, 426, 456, 497)
- Modify: `internal/agent/act.go` (line ~297)

- [ ] **Step 1: Add emitter calls around PERCEIVE phase**

In `internal/agent/cognitive.go`, around line 316 where `registerSubtask` is called for PERCEIVE, add emitter calls:

Before:
```go
	donePerceive := ca.registerSubtask(ctx, parentTaskID, "PERCEIVE phase")
	state, err := ca.perceiver.Run(ctx, sess, msg.Text, msg.UserID)
	donePerceive()
```

After:
```go
	if ca.dashEmitter != nil {
		ca.dashEmitter.EmitPhaseStart(sess.ID, "PERCEIVE")
	}
	perceiveStart := time.Now()
	donePerceive := ca.registerSubtask(ctx, parentTaskID, "PERCEIVE phase")
	state, err := ca.perceiver.Run(ctx, sess, msg.Text, msg.UserID)
	donePerceive()
	if ca.dashEmitter != nil {
		ca.dashEmitter.EmitPhaseEnd(sess.ID, "PERCEIVE", time.Since(perceiveStart).Milliseconds())
	}
```

- [ ] **Step 2: Add emitter calls around PLAN phase**

Around line 426:

Before:
```go
		donePlan := ca.registerSubtask(ctx, parentTaskID, fmt.Sprintf("PLAN phase (attempt %d)", attempt))
		plan, err = ca.planner.Run(ctx, state)
		donePlan()
```

After:
```go
		if ca.dashEmitter != nil {
			ca.dashEmitter.EmitPhaseStart(sess.ID, "PLAN")
		}
		planStart := time.Now()
		donePlan := ca.registerSubtask(ctx, parentTaskID, fmt.Sprintf("PLAN phase (attempt %d)", attempt))
		plan, err = ca.planner.Run(ctx, state)
		donePlan()
		if ca.dashEmitter != nil {
			ca.dashEmitter.EmitPhaseEnd(sess.ID, "PLAN", time.Since(planStart).Milliseconds())
		}
```

- [ ] **Step 3: Add emitter calls around ACT phase**

Around line 456:

Before:
```go
		doneAct := ca.registerSubtask(ctx, parentTaskID, "ACT phase")
		taskCtx := NewTaskContext(fmt.Sprintf("task_%d", time.Now().UnixNano()), state.UserMessage)
		observations, actErr := ca.executor.RunWithContext(ctx, ch, sess, target, plan, taskCtx, rlState, episodeCollector)
		doneAct()
```

After:
```go
		if ca.dashEmitter != nil {
			ca.dashEmitter.EmitPhaseStart(sess.ID, "ACT")
		}
		actStart := time.Now()
		doneAct := ca.registerSubtask(ctx, parentTaskID, "ACT phase")
		taskCtx := NewTaskContext(fmt.Sprintf("task_%d", time.Now().UnixNano()), state.UserMessage)
		observations, actErr := ca.executor.RunWithContext(ctx, ch, sess, target, plan, taskCtx, rlState, episodeCollector)
		doneAct()
		if ca.dashEmitter != nil {
			ca.dashEmitter.EmitPhaseEnd(sess.ID, "ACT", time.Since(actStart).Milliseconds())
		}
```

- [ ] **Step 4: Add emitter calls around REFLECT phase**

Around line 497:

Before:
```go
		doneReflect := ca.registerSubtask(ctx, parentTaskID, "REFLECT phase")
		reflection, err = ca.reflector.Run(ctx, ch, target, state, plan, obsResult, attempt)
		doneReflect()
```

After:
```go
		if ca.dashEmitter != nil {
			ca.dashEmitter.EmitPhaseStart(sess.ID, "REFLECT")
		}
		reflectStart := time.Now()
		doneReflect := ca.registerSubtask(ctx, parentTaskID, "REFLECT phase")
		reflection, err = ca.reflector.Run(ctx, ch, target, state, plan, obsResult, attempt)
		doneReflect()
		if ca.dashEmitter != nil {
			ca.dashEmitter.EmitPhaseEnd(sess.ID, "REFLECT", time.Since(reflectStart).Milliseconds())
		}
```

- [ ] **Step 5: Add emitter calls for tool execution in act.go**

In `internal/agent/act.go`, around line 297 where `t.Execute` is called, the executor needs the emitter reference. The `Executor` struct is created in `cognitive.go` — add a `dashEmitter` field to it. Then around the `t.Execute` call:

In `act.go`, the tool execution is inside a method on the `Executor` (or similar struct). First, add a `dashEmitter DashboardEmitter` field to whatever struct owns the `executeSubTask` method. Then propagate it when the struct is created (from `CognitiveAgent`'s emitter).

Around the `t.Execute` call (line ~297), the variables in scope are: `toolName` (the tool name string), `toolInput` (the input string), and the session is available via the function's parameters. Find the session ID variable in scope (likely `sessionID` or accessed from a context/parameter).

Before:
```go
	start := time.Now()
	result, execErr := t.Execute(ctx, []byte(toolInput))
	durationMs := time.Since(start).Milliseconds()
```

After (adapt variable names to match the actual code):
```go
	if ex.dashEmitter != nil {
		ex.dashEmitter.EmitToolStart(sessionID, toolName, toolInput)
	}
	start := time.Now()
	result, execErr := t.Execute(ctx, []byte(toolInput))
	durationMs := time.Since(start).Milliseconds()
	if ex.dashEmitter != nil {
		ex.dashEmitter.EmitToolEnd(sessionID, toolName, execErr == nil && result.Error == "", durationMs)
	}
```

The exact struct and variable names must be read from `act.go` at implementation time — the tool name is `toolName` or `obs.ToolName`, the session ID flows through the function parameters or is on the executor struct.

- [ ] **Step 6: Verify compilation**

Run: `CGO_ENABLED=1 go build -tags fts5 ./internal/agent/`
Expected: success

- [ ] **Step 7: Run existing tests to check for regressions**

Run: `CGO_ENABLED=1 go test -tags fts5 ./internal/agent/ -v -count=1`
Expected: All existing tests PASS

- [ ] **Step 8: Commit**

```bash
git add internal/agent/cognitive.go internal/agent/act.go
git commit -m "feat(agent): emit dashboard events at phase transitions and tool calls"
```

---

### Task 10: Gateway Integration

**Files:**
- Create: `internal/gateway/init_dashboard.go`
- Create: `internal/dashboard/embed.go`
- Modify: `internal/gateway/gateway.go`
- Delete: `internal/gateway/http.go`

- [ ] **Step 1: Create embed.go with placeholder for web dist**

```go
// internal/dashboard/embed.go
package dashboard

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var webDistFS embed.FS

func WebDistFS() fs.FS {
	sub, err := fs.Sub(webDistFS, "dist")
	if err != nil {
		panic("dashboard: embedded dist not found: " + err.Error())
	}
	return sub
}
```

Create a minimal placeholder so the embed compiles:

```bash
mkdir -p internal/dashboard/dist
echo '<html><body><h1>IronClaw Dashboard</h1><p>Build the frontend: cd web && npm run build</p></body></html>' > internal/dashboard/dist/index.html
```

- [ ] **Step 2: Create init_dashboard.go**

```go
// internal/gateway/init_dashboard.go
package gateway

import (
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/cogmetrics"
	"github.com/Forest-Isle/IronClaw/internal/dashboard"
)

func (gw *Gateway) initDashboard() error {
	if !gw.cfg.Dashboard.Enabled {
		return nil
	}

	gw.dashboardBus = dashboard.NewBus(256)
	gw.stateTracker = dashboard.NewAgentStateTracker(gw.dashboardBus)
	go gw.stateTracker.Run()

	// Register evolution bridge
	if gw.evoEngine != nil && gw.evoEngine.IsEnabled() {
		gw.evoEngine.RegisterHook(dashboard.NewEvolutionBridge(gw.dashboardBus))
	}

	// Register cogmetrics collector as evolution hook
	if gw.evoEngine != nil && gw.evoEngine.IsEnabled() {
		gw.cogCollector = cogmetrics.NewCollector()
		gw.evoEngine.RegisterHook(gw.cogCollector)
	}

	// Inject emitter into agent runtimes
	emitter := dashboard.NewEmitter(gw.dashboardBus)
	gw.runtime.SetDashboardEmitter(emitter)
	if gw.cognitiveAgent != nil {
		gw.cognitiveAgent.SetDashboardEmitter(emitter)
	}

	// WebSocket hub
	gw.dashboardHub = dashboard.NewHub(gw.dashboardBus)
	go gw.dashboardHub.Run()

	// HTTP server
	go dashboard.StartServer(gw.cfg.Dashboard, dashboard.ServerDeps{
		DB:        gw.db,
		Hub:       gw.dashboardHub,
		Tracker:   gw.stateTracker,
		Collector: gw.cogCollector,
		StaticFS:  dashboard.WebDistFS(),
	})

	slog.Info("dashboard initialized", "addr", gw.cfg.Dashboard.Addr)
	return nil
}
```

- [ ] **Step 3: Add dashboard fields to Gateway struct**

In `internal/gateway/gateway.go`, add fields to the `Gateway` struct (after `staleDetector` field):

```go
dashboardBus    *dashboard.Bus
dashboardHub    *dashboard.Hub
stateTracker    *dashboard.AgentStateTracker
cogCollector    *cogmetrics.Collector
```

Add imports for `dashboard` and `cogmetrics` packages.

- [ ] **Step 4: Call initDashboard in New()**

In `internal/gateway/gateway.go`, in the `New()` function, add after the `gw.sched.SetHandler(...)` block (around line 137, before `return gw, nil`):

```go
if err := gw.initDashboard(); err != nil {
	return nil, fmt.Errorf("dashboard: %w", err)
}
```

- [ ] **Step 5: Update Start() — keep old HTTP server as fallback**

In `internal/gateway/gateway.go` `Start()` method (around line 190), change:

Before:
```go
if gw.cfg.Server.Enabled {
	go startHTTPServer(gw.cfg.Server.Addr, gw.db)
}
```

After:
```go
if gw.cfg.Server.Enabled && !gw.cfg.Dashboard.Enabled {
	go startHTTPServer(gw.cfg.Server.Addr, gw.db)
}
```

This keeps backward compatibility: if only `server.enabled` is true and `dashboard.enabled` is false, the old lightweight HTTP server still works.

- [ ] **Step 6: Add cleanup to Stop()**

In `internal/gateway/gateway.go` `Stop()` method, add before `_ = gw.db.Close()`:

```go
if gw.dashboardHub != nil {
	gw.dashboardHub.Stop()
}
if gw.stateTracker != nil {
	gw.stateTracker.Stop()
}
```

- [ ] **Step 7: Verify compilation**

Run: `CGO_ENABLED=1 go build -tags fts5 ./cmd/ironclaw/`
Expected: success

- [ ] **Step 8: Commit**

```bash
git add internal/dashboard/embed.go internal/dashboard/dist/index.html internal/gateway/init_dashboard.go internal/gateway/gateway.go
git commit -m "feat(gateway): wire dashboard subsystem into gateway lifecycle"
```

---

### Task 11: Frontend SPA Setup

**Files:**
- Create: `web/package.json`, `web/vite.config.ts`, `web/tsconfig.json`, `web/index.html`, `web/src/main.tsx`

- [ ] **Step 1: Initialize the web project**

```bash
mkdir -p web/src
cd web
npm init -y
npm install preact
npm install -D vite @preact/preset-vite typescript
```

- [ ] **Step 2: Create vite.config.ts**

```typescript
// web/vite.config.ts
import { defineConfig } from 'vite'
import preact from '@preact/preset-vite'

export default defineConfig({
  plugins: [preact()],
  build: {
    outDir: '../internal/dashboard/dist',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:8080',
      '/ws': {
        target: 'ws://127.0.0.1:8080',
        ws: true,
      },
      '/health': 'http://127.0.0.1:8080',
    },
  },
})
```

- [ ] **Step 3: Create tsconfig.json**

```json
{
  "compilerOptions": {
    "target": "ES2020",
    "module": "ESNext",
    "lib": ["ES2020", "DOM", "DOM.Iterable"],
    "jsx": "react-jsx",
    "jsxImportSource": "preact",
    "moduleResolution": "bundler",
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "forceConsistentCasingInFileNames": true,
    "outDir": "dist"
  },
  "include": ["src"]
}
```

- [ ] **Step 4: Create index.html**

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>IronClaw Dashboard</title>
</head>
<body>
  <div id="app"></div>
  <script type="module" src="/src/main.tsx"></script>
</body>
</html>
```

- [ ] **Step 5: Create main.tsx entry point**

```tsx
// web/src/main.tsx
import { render } from 'preact'
import { App } from './app'
import './styles/global.css'

render(<App />, document.getElementById('app')!)
```

- [ ] **Step 6: Create minimal app.tsx**

```tsx
// web/src/app.tsx
export function App() {
  return <div><h1>IronClaw Dashboard</h1><p>Loading...</p></div>
}
```

- [ ] **Step 7: Create global.css**

```css
/* web/src/styles/global.css */
:root {
  --bg-primary: #0d1117;
  --bg-secondary: #161b22;
  --bg-tertiary: #21262d;
  --border: #30363d;
  --text-primary: #e6edf3;
  --text-secondary: #8b949e;
  --accent: #58a6ff;
  --success: #3fb950;
  --warning: #d29922;
  --error: #f85149;
  --font-mono: 'SF Mono', 'Fira Code', monospace;
}

*, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }

body {
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif;
  background: var(--bg-primary);
  color: var(--text-primary);
  line-height: 1.5;
}

a { color: var(--accent); text-decoration: none; }
```

- [ ] **Step 8: Verify build**

```bash
cd web && npm run build
```

Expected: Files output to `internal/dashboard/dist/`

- [ ] **Step 9: Add web/ to .gitignore sensibly**

Add to `.gitignore`:
```
web/node_modules/
```

- [ ] **Step 10: Commit**

```bash
git add web/package.json web/package-lock.json web/vite.config.ts web/tsconfig.json web/index.html web/src/ .gitignore
git commit -m "feat(web): scaffold Preact + Vite frontend project"
```

---

### Task 12: Frontend Core Components

**Files:**
- Create: `web/src/lib/types.ts`, `web/src/lib/api.ts`
- Create: `web/src/hooks/useWebSocket.ts`, `web/src/hooks/useAgentState.ts`
- Create: `web/src/components/Layout.tsx`, `web/src/components/AgentStatus.tsx`, `web/src/components/PhaseTimeline.tsx`, `web/src/components/ToolCallFeed.tsx`, `web/src/components/SessionList.tsx`
- Create: `web/src/pages/Overview.tsx`, `web/src/pages/NotFound.tsx`
- Modify: `web/src/app.tsx`

- [ ] **Step 1: Install wouter for routing**

```bash
cd web && npm install wouter
```

- [ ] **Step 2: Create types.ts**

```typescript
// web/src/lib/types.ts
export type EventType =
  | 'phase.start' | 'phase.end'
  | 'tool.start' | 'tool.end'
  | 'plan.generated' | 'replan.start'
  | 'task.update'
  | 'session.start' | 'session.end'
  | 'agent.idle'

export interface DashboardEvent {
  type: EventType
  timestamp: string
  session_id?: string
  data: Record<string, unknown>
}

export interface SessionState {
  session_id: string
  channel?: string
  current_phase: string
  current_tool?: string
  phase_started_at?: string
  tools_executed: number
  replan_count: number
}

export interface StateSnapshot {
  status: 'idle' | 'busy'
  active_sessions: SessionState[]
  uptime_seconds: number
  total_sessions_today: number
}

export interface ToolEvent {
  timestamp: string
  tool_name: string
  succeeded?: boolean
  duration_ms?: number
  running: boolean
}

export interface PhaseEvent {
  phase: string
  started_at: string
  duration_ms?: number
  running: boolean
}
```

- [ ] **Step 3: Create api.ts**

```typescript
// web/src/lib/api.ts
const BASE = ''

export async function fetchAgentState(): Promise<import('./types').StateSnapshot> {
  const res = await fetch(`${BASE}/api/agent/state`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function fetchSessions() {
  const res = await fetch(`${BASE}/api/sessions`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}
```

- [ ] **Step 4: Create useWebSocket.ts**

```typescript
// web/src/hooks/useWebSocket.ts
import { useEffect, useRef, useState, useCallback } from 'preact/hooks'
import type { DashboardEvent } from '../lib/types'

type ConnectionStatus = 'connected' | 'reconnecting' | 'disconnected'

export function useWebSocket(onEvent: (ev: DashboardEvent) => void) {
  const [status, setStatus] = useState<ConnectionStatus>('disconnected')
  const wsRef = useRef<WebSocket | null>(null)
  const retriesRef = useRef(0)

  const connect = useCallback(() => {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = `${proto}//${location.host}/ws`
    const ws = new WebSocket(url)
    wsRef.current = ws

    ws.onopen = () => {
      setStatus('connected')
      retriesRef.current = 0
    }

    ws.onmessage = (msg) => {
      try {
        const ev: DashboardEvent = JSON.parse(msg.data)
        onEvent(ev)
      } catch { /* ignore malformed */ }
    }

    ws.onclose = () => {
      setStatus('reconnecting')
      const delay = Math.min(1000 * Math.pow(2, retriesRef.current), 30000)
      retriesRef.current++
      setTimeout(connect, delay)
    }

    ws.onerror = () => ws.close()
  }, [onEvent])

  useEffect(() => {
    connect()
    return () => { wsRef.current?.close() }
  }, [connect])

  return status
}
```

- [ ] **Step 5: Create useAgentState.ts**

```typescript
// web/src/hooks/useAgentState.ts
import { useReducer, useEffect, useCallback } from 'preact/hooks'
import type { DashboardEvent, StateSnapshot, ToolEvent, PhaseEvent } from '../lib/types'
import { fetchAgentState } from '../lib/api'
import { useWebSocket } from './useWebSocket'

interface AgentState {
  status: 'idle' | 'busy'
  activeSessions: StateSnapshot['active_sessions']
  recentTools: ToolEvent[]
  phaseHistory: PhaseEvent[]
  connected: boolean
  totalSessionsToday: number
  uptimeSeconds: number
}

type Action =
  | { type: 'snapshot'; data: StateSnapshot }
  | { type: 'event'; data: DashboardEvent }
  | { type: 'connection'; connected: boolean }

const MAX_TOOLS = 100

function reducer(state: AgentState, action: Action): AgentState {
  switch (action.type) {
    case 'snapshot':
      return {
        ...state,
        status: action.data.status,
        activeSessions: action.data.active_sessions || [],
        totalSessionsToday: action.data.total_sessions_today,
        uptimeSeconds: action.data.uptime_seconds,
      }

    case 'connection':
      return { ...state, connected: action.connected }

    case 'event': {
      const ev = action.data
      let { activeSessions, recentTools, phaseHistory, status, totalSessionsToday } = state

      switch (ev.type) {
        case 'phase.start':
          phaseHistory = [...phaseHistory, {
            phase: ev.data.phase as string,
            started_at: ev.timestamp,
            running: true,
          }]
          status = 'busy'
          break

        case 'phase.end':
          phaseHistory = phaseHistory.map(p =>
            p.phase === ev.data.phase && p.running
              ? { ...p, running: false, duration_ms: ev.data.duration_ms as number }
              : p
          )
          break

        case 'tool.start':
          recentTools = [{
            timestamp: ev.timestamp,
            tool_name: ev.data.tool_name as string,
            running: true,
          }, ...recentTools].slice(0, MAX_TOOLS)
          break

        case 'tool.end':
          recentTools = recentTools.map(t =>
            t.tool_name === ev.data.tool_name && t.running
              ? { ...t, running: false, succeeded: ev.data.succeeded as boolean, duration_ms: ev.data.duration_ms as number }
              : t
          )
          break

        case 'session.end':
          status = 'idle'
          phaseHistory = []
          totalSessionsToday++
          break
      }

      return { ...state, activeSessions, recentTools, phaseHistory, status, totalSessionsToday }
    }

    default:
      return state
  }
}

const initialState: AgentState = {
  status: 'idle',
  activeSessions: [],
  recentTools: [],
  phaseHistory: [],
  connected: false,
  totalSessionsToday: 0,
  uptimeSeconds: 0,
}

export function useAgentState() {
  const [state, dispatch] = useReducer(reducer, initialState)

  const onEvent = useCallback((ev: DashboardEvent) => {
    dispatch({ type: 'event', data: ev })
  }, [])

  const wsStatus = useWebSocket(onEvent)

  useEffect(() => {
    dispatch({ type: 'connection', connected: wsStatus === 'connected' })
  }, [wsStatus])

  useEffect(() => {
    fetchAgentState()
      .then(data => dispatch({ type: 'snapshot', data }))
      .catch(() => {})
  }, [])

  return { ...state, wsStatus }
}
```

- [ ] **Step 6: Create Layout.tsx**

```tsx
// web/src/components/Layout.tsx
import type { ComponentChildren } from 'preact'

export function Layout({ children, connected }: { children: ComponentChildren; connected: boolean }) {
  return (
    <div style={{ display: 'flex', minHeight: '100vh' }}>
      <nav style={{
        width: 200, padding: '20px 16px',
        background: 'var(--bg-secondary)', borderRight: '1px solid var(--border)',
      }}>
        <h2 style={{ fontSize: 16, marginBottom: 24 }}>IronClaw</h2>
        <a href="/" style={{ display: 'block', padding: '8px 12px', borderRadius: 6, background: 'var(--bg-tertiary)' }}>
          Overview
        </a>
      </nav>
      <main style={{ flex: 1, padding: 24 }}>
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 16 }}>
          <span style={{
            display: 'inline-flex', alignItems: 'center', gap: 6,
            fontSize: 13, color: 'var(--text-secondary)',
          }}>
            <span style={{
              width: 8, height: 8, borderRadius: '50%',
              background: connected ? 'var(--success)' : 'var(--error)',
            }} />
            {connected ? 'Connected' : 'Disconnected'}
          </span>
        </div>
        {children}
      </main>
    </div>
  )
}
```

- [ ] **Step 7: Create AgentStatus.tsx**

```tsx
// web/src/components/AgentStatus.tsx
import type { StateSnapshot } from '../lib/types'

export function AgentStatus({ status, sessions }: {
  status: 'idle' | 'busy'
  sessions: StateSnapshot['active_sessions']
}) {
  const session = sessions[0]
  return (
    <div style={{
      padding: 20, background: 'var(--bg-secondary)',
      border: '1px solid var(--border)', borderRadius: 8, marginBottom: 16,
    }}>
      <h3 style={{ fontSize: 14, color: 'var(--text-secondary)', marginBottom: 12 }}>Agent Status</h3>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <span style={{
          padding: '4px 12px', borderRadius: 12, fontSize: 13, fontWeight: 600,
          background: status === 'busy' ? 'rgba(88,166,255,0.15)' : 'var(--bg-tertiary)',
          color: status === 'busy' ? 'var(--accent)' : 'var(--text-secondary)',
        }}>
          {status.toUpperCase()}
        </span>
        {session && (
          <span style={{ fontSize: 14 }}>
            Phase: <strong>{session.current_phase || '—'}</strong>
            {session.current_tool && <> ▸ tool: <code>{session.current_tool}</code></>}
          </span>
        )}
      </div>
      {session && (
        <div style={{ marginTop: 8, fontSize: 13, color: 'var(--text-secondary)' }}>
          Session: {session.session_id.slice(0, 8)} ({session.channel || 'unknown'})
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 8: Create PhaseTimeline.tsx**

```tsx
// web/src/components/PhaseTimeline.tsx
import type { PhaseEvent } from '../lib/types'

const PHASE_ORDER = ['PERCEIVE', 'PLAN', 'ACT', 'OBSERVE', 'REFLECT']

export function PhaseTimeline({ phases }: { phases: PhaseEvent[] }) {
  return (
    <div style={{
      padding: 20, background: 'var(--bg-secondary)',
      border: '1px solid var(--border)', borderRadius: 8, marginBottom: 16,
    }}>
      <h3 style={{ fontSize: 14, color: 'var(--text-secondary)', marginBottom: 12 }}>Phase Timeline</h3>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
        {PHASE_ORDER.map((name, i) => {
          const phase = phases.find(p => p.phase === name)
          const isActive = phase?.running
          const isDone = phase && !phase.running
          return (
            <>
              {i > 0 && <span style={{ color: 'var(--text-secondary)' }}>→</span>}
              <span style={{
                padding: '4px 12px', borderRadius: 6, fontSize: 13, fontFamily: 'var(--font-mono)',
                background: isActive ? 'rgba(88,166,255,0.15)' : isDone ? 'var(--bg-tertiary)' : 'transparent',
                color: isActive ? 'var(--accent)' : isDone ? 'var(--text-primary)' : 'var(--text-secondary)',
                border: isActive ? '1px solid var(--accent)' : '1px solid transparent',
              }}>
                {isActive && '▸ '}{name}
                {isDone && phase.duration_ms != null && (
                  <span style={{ marginLeft: 6, fontSize: 11, color: 'var(--text-secondary)' }}>
                    {phase.duration_ms}ms
                  </span>
                )}
                {isActive && <span style={{ marginLeft: 6, fontSize: 11 }}>running…</span>}
              </span>
            </>
          )
        })}
      </div>
    </div>
  )
}
```

- [ ] **Step 9: Create ToolCallFeed.tsx**

```tsx
// web/src/components/ToolCallFeed.tsx
import type { ToolEvent } from '../lib/types'

export function ToolCallFeed({ tools }: { tools: ToolEvent[] }) {
  return (
    <div style={{
      padding: 20, background: 'var(--bg-secondary)',
      border: '1px solid var(--border)', borderRadius: 8, marginBottom: 16,
      maxHeight: 300, overflowY: 'auto',
    }}>
      <h3 style={{ fontSize: 14, color: 'var(--text-secondary)', marginBottom: 12 }}>Tool Calls</h3>
      {tools.length === 0 && (
        <div style={{ fontSize: 13, color: 'var(--text-secondary)' }}>No tool calls yet</div>
      )}
      {tools.map((t, i) => (
        <div key={i} style={{
          display: 'flex', gap: 12, padding: '6px 0',
          borderBottom: '1px solid var(--border)', fontSize: 13,
        }}>
          <span style={{ color: 'var(--text-secondary)', fontFamily: 'var(--font-mono)', minWidth: 70 }}>
            {new Date(t.timestamp).toLocaleTimeString()}
          </span>
          <code style={{ minWidth: 100 }}>{t.tool_name}</code>
          {t.running ? (
            <span style={{ color: 'var(--warning)' }}>⏳ running…</span>
          ) : (
            <span style={{ color: t.succeeded ? 'var(--success)' : 'var(--error)' }}>
              {t.succeeded ? '✓' : '✗'} {t.duration_ms}ms
            </span>
          )}
        </div>
      ))}
    </div>
  )
}
```

- [ ] **Step 10: Create SessionList.tsx**

```tsx
// web/src/components/SessionList.tsx
import type { StateSnapshot } from '../lib/types'

export function SessionList({ sessions, total }: {
  sessions: StateSnapshot['active_sessions']
  total: number
}) {
  return (
    <div style={{
      padding: 20, background: 'var(--bg-secondary)',
      border: '1px solid var(--border)', borderRadius: 8,
    }}>
      <h3 style={{ fontSize: 14, color: 'var(--text-secondary)', marginBottom: 12 }}>
        Sessions ({total} today)
      </h3>
      {sessions.length === 0 && (
        <div style={{ fontSize: 13, color: 'var(--text-secondary)' }}>No active sessions</div>
      )}
      {sessions.map(s => (
        <div key={s.session_id} style={{
          display: 'flex', justifyContent: 'space-between', padding: '8px 0',
          borderBottom: '1px solid var(--border)', fontSize: 13,
        }}>
          <span><code>{s.session_id.slice(0, 8)}</code> ({s.channel || '?'})</span>
          <span style={{ color: 'var(--text-secondary)' }}>
            {s.tools_executed} tools · {s.replan_count} replans
          </span>
        </div>
      ))}
    </div>
  )
}
```

- [ ] **Step 11: Create Overview page**

```tsx
// web/src/pages/Overview.tsx
import { useAgentState } from '../hooks/useAgentState'
import { Layout } from '../components/Layout'
import { AgentStatus } from '../components/AgentStatus'
import { PhaseTimeline } from '../components/PhaseTimeline'
import { ToolCallFeed } from '../components/ToolCallFeed'
import { SessionList } from '../components/SessionList'

export function Overview() {
  const state = useAgentState()

  return (
    <Layout connected={state.connected}>
      <AgentStatus status={state.status} sessions={state.activeSessions} />
      <PhaseTimeline phases={state.phaseHistory} />
      <ToolCallFeed tools={state.recentTools} />
      <SessionList sessions={state.activeSessions} total={state.totalSessionsToday} />
      <div style={{
        marginTop: 24, padding: '12px 20px', background: 'var(--bg-secondary)',
        borderRadius: 8, fontSize: 13, color: 'var(--text-secondary)',
        display: 'flex', gap: 24,
      }}>
        <span>Uptime: {Math.floor(state.uptimeSeconds / 3600)}h {Math.floor((state.uptimeSeconds % 3600) / 60)}m</span>
      </div>
    </Layout>
  )
}
```

- [ ] **Step 12: Create NotFound page and update app.tsx**

```tsx
// web/src/pages/NotFound.tsx
export function NotFound() {
  return (
    <div style={{ padding: 40, textAlign: 'center' }}>
      <h1>404</h1>
      <p><a href="/">Back to Dashboard</a></p>
    </div>
  )
}
```

```tsx
// web/src/app.tsx (replace contents)
import { Route, Switch } from 'wouter'
import { Overview } from './pages/Overview'
import { NotFound } from './pages/NotFound'

export function App() {
  return (
    <Switch>
      <Route path="/" component={Overview} />
      <Route component={NotFound} />
    </Switch>
  )
}
```

- [ ] **Step 13: Verify frontend builds**

```bash
cd web && npm run build
```

Expected: Build succeeds, files in `internal/dashboard/dist/`

- [ ] **Step 14: Commit**

```bash
git add web/src/ web/package.json web/package-lock.json
git commit -m "feat(web): add Overview page with real-time agent monitoring"
```

---

### Task 13: Build Integration

**Files:**
- Modify: `Makefile`
- Modify: `internal/dashboard/dist/` (now auto-generated by web build)

- [ ] **Step 1: Update Makefile**

Add `web` target and make `build` depend on it. In the Makefile, add before the `build` target:

```makefile
## web: Build frontend assets
web:
	@cd web && npm ci --prefer-offline && npm run build
```

Modify the `build` target to depend on `web`:

```makefile
build: web
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/ironclaw
```

- [ ] **Step 2: Add npm build script to web/package.json**

Ensure `web/package.json` has in `"scripts"`:

```json
{
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "preview": "vite preview"
  }
}
```

- [ ] **Step 3: Update .gitignore**

Add:
```
web/node_modules/
internal/dashboard/dist/
```

Remove the placeholder `internal/dashboard/dist/index.html` from git (it's now generated):

```bash
git rm --cached internal/dashboard/dist/index.html 2>/dev/null || true
```

- [ ] **Step 4: Verify full build**

```bash
make clean && make build
```

Expected: Frontend builds first, then Go binary compiles with embedded assets

- [ ] **Step 5: Verify the binary runs**

```bash
./bin/ironclaw version
```

Expected: Prints version info (binary compiled correctly)

- [ ] **Step 6: Commit**

```bash
git add Makefile web/package.json .gitignore
git commit -m "feat(build): integrate frontend build into Makefile"
```

---

### Task 14: End-to-End Smoke Test

- [ ] **Step 1: Create a test config with dashboard enabled**

Create a temporary config file:

```yaml
# /tmp/ironclaw-test.yaml
llm:
  provider: anthropic
  api_key: "${ANTHROPIC_API_KEY}"
  model: claude-sonnet-4-20250514
dashboard:
  enabled: true
  addr: "127.0.0.1:9090"
  token: ""
store:
  path: "/tmp/ironclaw-test.db"
```

- [ ] **Step 2: Start the binary**

```bash
./bin/ironclaw start --config /tmp/ironclaw-test.yaml &
sleep 2
```

- [ ] **Step 3: Verify dashboard is accessible**

```bash
curl -s http://127.0.0.1:9090/health | grep -q '"status":"ok"'
echo "Health check: $?"

curl -s http://127.0.0.1:9090/api/agent/state | grep -q '"status"'
echo "Agent state: $?"

curl -s http://127.0.0.1:9090/ | grep -q 'IronClaw'
echo "SPA served: $?"
```

Expected: All echo `0` (success)

- [ ] **Step 4: Stop the binary and clean up**

```bash
kill %1 2>/dev/null
rm -f /tmp/ironclaw-test.db /tmp/ironclaw-test.yaml
```

- [ ] **Step 5: Run full test suite**

```bash
CGO_ENABLED=1 go test -tags fts5 ./internal/dashboard/ -v
CGO_ENABLED=1 go test -tags fts5 ./internal/agent/ -v
CGO_ENABLED=1 go test -tags fts5 ./internal/gateway/ -v
```

Expected: All PASS

- [ ] **Step 6: Commit (if any adjustments)**

```bash
git add -A
git commit -m "feat(dashboard): complete web dashboard v1 with real-time agent monitoring"
```
