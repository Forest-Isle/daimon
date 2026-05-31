# IronClaw Phase 2: Unified Agent + Event Bus — Design Spec

**Date**: 2026-05-31
**Status**: approved
**Scope**: Delete Runtime/CognitiveAgent, replace with Agent + LoopStrategy + EventBus
**Depends on**: Phase 1 (AgentDeps migration)

---

## 1. Motivation

Phase 1 eliminated Setter Hell by introducing AgentDeps. But the core duplication remains:

- `Runtime` (884 lines) and `CognitiveAgent` (1712 lines) are two independent types that do the same thing — process messages through an LLM loop. They share deps but duplicate session management, tool execution, context compression, streaming, and event emission.
- `DashboardEmitter` (12-method interface + MultiEmitter wrapper) requires every component to nil-check and explicitly forward events. Adding a new event type means changing the interface + all 3 implementations.
- Switching between modes requires `switch gw.currentMode` in handleInbound, and the three modes (simple/cognitive/graph) have completely different dispatch paths.

Phase 2 unifies the agent abstraction and replaces ad-hoc event emission with a proper pub/sub bus.

## 2. Design

### 2.1 Agent — Single Entry Point

**New file**: `internal/agent/agent.go`

```go
type Agent struct {
    deps       AgentDeps
    strategy   LoopStrategy
    approvalFn ApprovalFunc
    eventBus   EventBus
}

func NewAgent(deps AgentDeps, strategy LoopStrategy, bus EventBus) *Agent {
    return &Agent{deps: deps, strategy: strategy, eventBus: bus}
}

// HandleMessage is the sole entry point for all agent modes.
func (a *Agent) HandleMessage(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) error {
    sess, _ := a.deps.Core.Sessions.Get(ctx, msg.Channel, msg.ChannelID)
    sess.AddMessage(session.Message{Role: "user", Content: msg.Text, ...})
    
    systemPrompt := a.buildSystemPrompt(ctx, msg.Text)
    
    // Compress context
    a.deps.Memory.ContextMgr.Compress(ctx, sess, systemPrompt)
    
    // Emit session start
    a.eventBus.Publish(SessionStarted{SessionID: sess.ID, Channel: msg.Channel})
    
    // Delegate to strategy
    start := time.Now()
    err := a.strategy.Execute(ctx, a, ch, msg, sess, systemPrompt)
    
    // Emit session end
    a.eventBus.Publish(SessionEnded{SessionID: sess.ID, Succeeded: err == nil, DurationMs: time.Since(start).Milliseconds()})
    
    // Persist
    a.deps.Core.Sessions.Persist(ctx, sess)
    
    return err
}
```

All common methods live on Agent:
- `buildSystemPrompt` — personality + memories + skills + profile  
- `executeToolCall` — single tool through interceptor chain, emits ToolExecuted
- `streamResponse` — LLM streaming with speculative execution, emits MetricsTick

### 2.2 LoopStrategy — Three Implementations

**New file**: `internal/agent/loop_strategy.go` (interface definition)

```go
type LoopStrategy interface {
    Execute(ctx context.Context, a *Agent, ch channel.Channel, msg channel.InboundMessage, sess *session.Session, systemPrompt string) error
}
```

**New file**: `internal/agent/simple_loop.go` (~350 lines)

```go
type SimpleLoop struct{}

func (SimpleLoop) Execute(ctx context.Context, a *Agent, ch channel.Channel, msg channel.InboundMessage, sess *session.Session, systemPrompt string) error {
    target := channel.MessageTarget{Channel: msg.Channel, ChannelID: msg.ChannelID}
    maxIter := a.deps.Core.Cfg.MaxIterations
    
    for iter := 0; iter < maxIter; iter++ {
        // Stream LLM response
        // Collect tool calls
        // Execute tools (via a.executeToolCall)
        // If no tools, done
    }
    return nil
}
```

Logic is 1:1 copied from `Runtime.HandleMessage` (lines 234-603), adapted to use `a.deps.*` and `a.eventBus.Publish(...)`.

**New file**: `internal/agent/cognitive_loop.go` (~500 lines)

```go
type CognitiveLoop struct {
    perceiver *Perceiver
    planner   *Planner
    executor  *Executor
    observer  *Observer
    reflector *Reflector
    // Optional advanced planners
    mctsPlanner *MCTSPlanner
    treePlanner *StrategicTreePlanner
}

func NewCognitiveLoop(deps AgentDeps, opts *CognitiveAgentOptions) *CognitiveLoop {
    cl := &CognitiveLoop{
        perceiver: NewPerceiver(deps.Memory.Store, deps.Memory.BaseDir),
        planner:   NewPlanner(deps.Core.Provider, deps.Core.Tools, deps.Core.Cfg.Cognitive, deps.Core.LLMCfg.Model),
        executor:  NewExecutor(deps.Core.Provider, deps.Core.Tools, deps.Security.Interceptor, deps.Core.Cfg.Concurrent),
        observer:  NewObserver(deps.Core.Provider, deps.Core.Tools, deps.Core.LLMCfg.Model),
        reflector: NewReflector(deps.Core.Provider, deps.Core.LLMCfg.Model, deps.Memory.Store, deps.Memory.BaseDir, deps.Memory.FactExtractor, deps.Memory.LifecycleMgr, deps.Memory.Profiler),
    }
    if opts != nil {
        cl.mctsPlanner = opts.MCTSPlanner
        cl.treePlanner = opts.TreePlanner
        // ... wire remaining opts
    }
    return cl
}

func (cl *CognitiveLoop) Execute(ctx context.Context, a *Agent, ch channel.Channel, msg channel.InboundMessage, sess *session.Session, systemPrompt string) error {
    state := NewCognitiveState(msg.Text, sess.ID)
    
    // PERCEIVE
    a.eventBus.Publish(PhaseChanged{SessionID: sess.ID, Phase: "PERCEIVE", IsStart: true})
    state = cl.perceiver.Run(ctx, state)
    a.eventBus.Publish(PhaseChanged{SessionID: sess.ID, Phase: "PERCEIVE", IsStart: false})
    
    // PLAN
    a.eventBus.Publish(PhaseChanged{SessionID: sess.ID, Phase: "PLAN", IsStart: true})
    plan, _ := cl.planner.Run(ctx, state)
    a.eventBus.Publish(PlanGenerated{SessionID: sess.ID, TaskCount: len(plan.Tasks), Complexity: plan.Complexity})
    a.eventBus.Publish(PhaseChanged{SessionID: sess.ID, Phase: "PLAN", IsStart: false})
    
    // ACT → OBSERVE → REFLECT loop with replan
    for attempt := 0; attempt <= MaxReplanAttempts; attempt++ {
        // ACT
        for _, task := range plan.Tasks {
            a.eventBus.Publish(ToolStarted{SessionID: sess.ID, ToolName: task.Tool, Input: task.Input})
            result := a.executeToolCall(ctx, ch, sess, task)
            a.eventBus.Publish(ToolExecuted{SessionID: sess.ID, ToolName: task.Tool, Succeeded: result.Error == "", DurationMs: result.Duration.Milliseconds()})
        }
        
        // OBSERVE
        obs := cl.observer.Run(ctx, state, plan)
        a.eventBus.Publish(ObservationResult{...})
        
        // REFLECT
        if obs.AllPassed { break }
        a.eventBus.Publish(ReplanStarted{SessionID: sess.ID, Attempt: attempt, Reason: obs.FailureSummary})
        plan = cl.reflector.Replan(ctx, state, obs)
    }
    
    return nil
}
```

**Existing file to adapt**: `internal/agent/graph_loop.go` (from existing graph engine)

```go
type GraphLoop struct {
    store ExecutionEventStore
}

func (gl *GraphLoop) Execute(ctx context.Context, a *Agent, ch channel.Channel, msg channel.InboundMessage, sess *session.Session, systemPrompt string) error {
    // Existing graph execution logic, adapted to use a.deps and a.eventBus
}
```

### 2.3 EventBus — Typed Pub/Sub

**New file**: `internal/agent/events.go`

```go
type Event interface {
    EventType() string
}

// 12 event types:
type SessionStarted struct { SessionID, Channel string }
func (SessionStarted) EventType() string { return "session.started" }

type SessionEnded struct { SessionID string; Succeeded bool; DurationMs int64 }
func (SessionEnded) EventType() string { return "session.ended" }

type ToolExecuted struct { SessionID, ToolName string; Succeeded bool; DurationMs int64; Error string }
func (ToolExecuted) EventType() string { return "tool.executed" }

type PhaseChanged struct { SessionID, Phase string; IsStart bool }
func (PhaseChanged) EventType() string { return "phase.changed" }

type ContextCompressed struct { SessionID, Reason string; LayersRun int; BeforePct, AfterPct float64 }
func (ContextCompressed) EventType() string { return "context.compressed" }

type PlanGenerated struct { SessionID string; TaskCount int; Complexity string; HasDirectReply bool }
func (PlanGenerated) EventType() string { return "plan.generated" }

type ReplanStarted struct { SessionID string; Attempt int; Reason string }
func (ReplanStarted) EventType() string { return "replan.started" }

type ObservationCompleted struct { SessionID string; Passed, Failed, Total int; OverallProgress float64 }
func (ObservationCompleted) EventType() string { return "observation.completed" }

type SubAgentSpawned struct { SessionID, ParentSessionID, AgentName, Task string }
func (SubAgentSpawned) EventType() string { return "subagent.spawned" }

type SubAgentCompleted struct { SessionID, AgentName string; Succeeded bool; DurationMs int64 }
func (SubAgentCompleted) EventType() string { return "subagent.completed" }

type MetricsTick struct { SessionID string; Iteration, MaxIter int; Utilization float64; InputTokens, OutputTokens, CacheCreate, CacheRead int64; Model, Provider string }
func (MetricsTick) EventType() string { return "metrics.tick" }
```

**New file**: `internal/agent/inproc_bus.go`

```go
type EventBus interface {
    Publish(event Event)
    Subscribe(handler func(Event)) Subscription
}

type Subscription interface {
    Unsubscribe()
}

type inprocBus struct {
    mu   sync.RWMutex
    subs map[int]func(Event)
    next int
}

func NewEventBus() EventBus {
    return &inprocBus{subs: make(map[int]func(Event))}
}

// Publish is non-blocking: runs handlers in goroutines, drops if queue full.
func (b *inprocBus) Publish(event Event) {
    b.mu.RLock()
    defer b.mu.RUnlock()
    for _, h := range b.subs {
        go h(event) // fire-and-forget
    }
}

func (b *inprocBus) Subscribe(handler func(Event)) Subscription {
    b.mu.Lock()
    defer b.mu.Unlock()
    id := b.next
    b.next++
    b.subs[id] = handler
    return &subscription{bus: b, id: id}
}
```

### 2.4 Gateway Changes

**Delete**: The `switch gw.currentMode` dispatch in handleInbound. Replace with:

```go
func (gw *Gateway) handleInbound(ctx context.Context, msg channel.InboundMessage) {
    // Rate limit check
    // Command dispatch (via commandTable)
    // Agent dispatch:
    if err := gw.agent.HandleMessage(ctx, ch, msg); err != nil {
        slog.Error("agent error", "err", err)
    }
}
```

Gateway no longer knows about "simple", "cognitive", or "graph" modes. It has one Agent. Mode switching is done by replacing the Agent's strategy:

```go
func (gw *Gateway) SetMode(mode string) error {
    switch mode {
    case "simple":
        gw.agent.SetStrategy(&SimpleLoop{})
    case "cognitive":
        gw.agent.SetStrategy(gw.cognitiveLoop)
    case "graph":
        gw.agent.SetStrategy(gw.graphLoop)
    }
    return nil
}
```

### 2.5 SubAgentManager Changes

SubAgentManager creates Agent instances instead of Runtime:

```go
func (m *SubAgentManager) Spawn(ctx context.Context, req SpawnRequest) (*SubAgentResult, error) {
    subDeps := m.deps // clone with scoped tools
    subDeps.Core.Tools = scopedTools
    subDeps.Core.AgentID = req.Spec.Name
    
    agent := NewAgent(subDeps, &SimpleLoop{}, NewEventBus())
    // Run agent, extract results
}
```

### 2.6 Dashboard / TUI / Evolution Subscribers

Dashboard subscribes to EventBus and forwards to WebSocket:

```go
bus.Subscribe(func(e Event) {
    switch evt := e.(type) {
    case SessionStarted:
        hub.Broadcast(wsMessage{Type: "session_started", Data: evt})
    case ToolExecuted:
        stateTracker.RecordTool(evt)
        hub.Broadcast(wsMessage{Type: "tool_executed", Data: evt})
    // ... etc
    }
})
```

TUI subscribes for status bar updates:

```go
bus.Subscribe(func(e Event) {
    if m, ok := e.(MetricsTick); ok {
        tui.SendMetrics(RuntimeMetrics{...})
    }
})
```

Evolution engine subscribes for learning:

```go
bus.Subscribe(func(e Event) {
    if e, ok := e.(ToolExecuted); ok {
        engine.DispatchToolExec(e)
    }
})
```

## 3. File Inventory

### Delete (5 files)
| File | Lines | Reason |
|------|-------|--------|
| `internal/agent/runtime.go` | 884 | Merged into Agent + SimpleLoop |
| `internal/agent/cognitive.go` | 1712 | Merged into CognitiveLoop |
| `internal/agent/cognitive_v2.go` | 396 | Unused v2 experiment |
| `internal/agent/dashboard_emitter.go` | 148 | Replaced by EventBus (interface, MultiEmitter, RuntimeMetrics) |
| `internal/agent/stream.go` | 478 | Merged into Agent common methods |

### New (5 files)
| File | Lines | Purpose |
|------|-------|---------|
| `internal/agent/agent.go` | ~150 | Agent struct + HandleMessage + common methods |
| `internal/agent/loop_strategy.go` | ~10 | LoopStrategy interface |
| `internal/agent/simple_loop.go` | ~350 | SimpleLoop from Runtime logic |
| `internal/agent/cognitive_loop.go` | ~500 | CognitiveLoop from CognitiveAgent logic |
| `internal/agent/events.go` | ~120 | Event types + EventBus interface + Subscription |
| `internal/agent/inproc_bus.go` | ~80 | Non-blocking pub/sub implementation |

### Modify (~20 files)
| File | Changes |
|------|---------|
| `internal/agent/agent.go` | (new) |
| `internal/agent/perceive.go` | Remove Set* methods (use AgentDeps directly via CognitiveLoop) |
| `internal/agent/plan.go` | Unchanged (used by CognitiveLoop as-is) |
| `internal/agent/act.go` | Adapt: executeToolCall called via Agent, emit ToolExecuted via EventBus |
| `internal/agent/observe.go` | Unchanged |
| `internal/agent/reflect.go` | Unchanged (used by CognitiveLoop as-is) |
| `internal/agent/subagent.go` | Create Agent instead of Runtime |
| `internal/agent/concurrent.go` | Used by Agent.executeToolCall |
| `internal/agent/compression.go` | Used by Agent, emit ContextCompressed via EventBus |
| `internal/agent/autonomous_loop.go` | Delete Runtime references, use Agent |
| `internal/gateway/gateway.go` | Single agent field, no mode switch in handleInbound |
| `internal/gateway/init_agent.go` | Create Agent with strategy based on config mode |
| `internal/gateway/init_cognitive.go` | Create CognitiveLoop, not CognitiveAgent |
| `internal/gateway/init_multiagent.go` | SubAgentManager creates Agent instances |
| `internal/dashboard/emitter.go` | Delete DashboardEmitter impl, subscribe to EventBus |
| `internal/dashboard/state_tracker.go` | Subscribe to EventBus events |
| `internal/channel/tui/adapter.go` | Subscribe to MetricsTick events |
| `internal/evolution/engine.go` | Subscribe to ToolExecuted/SessionEnded events |
| `internal/eval/cognitive_runner.go` | Create Agent with CognitiveLoop |
| `internal/agent/*_test.go` | Adapt to Agent constructor |

### Unchanged
- `internal/tool/*`, `internal/memory/*`, `internal/knowledge/*`, `internal/sandbox/*`
- `internal/feature/*`, `internal/scheduler/*`, `internal/skill/*`
- `internal/store/*`, `internal/session/*`, `internal/config/*`
- `internal/mcp/*`, `internal/taskledger/*`

## 4. Risk Assessment

| Risk | Mitigation |
|------|------------|
| Logic regression in SimpleLoop | 1:1 copy from Runtime.HandleMessage, verified by tests |
| Logic regression in CognitiveLoop | 1:1 copy from CognitiveAgent phase methods |
| Event ordering (PhaseChanged before PlanGenerated) | Events published at exact same points as old Emit* calls |
| SubAgentManager Spawn broken | Agent created with scoped deps, same isolation logic |
| TUI status bar stops updating | Subscribe MetricsTick in TUI adapter init |
| Dashboard stops receiving events | Replace DashboardEmitter with EventBus subscriber |

## 5. Success Criteria

1. `go build ./...` succeeds
2. All 44 test packages pass
3. No `Runtime` type in codebase (except possibly in test helper code)
4. No `CognitiveAgent` type in codebase
5. No 12-method `DashboardEmitter` interface
6. `Agent.HandleMessage` is the sole message entry point
7. Gateway has one `agent *Agent` field (not `runtime` + `cognitiveAgent` + `graphEngine`)
8. EventBus subscribers exist for dashboard, TUI, and evolution

---

*End of Phase 2 design spec.*
