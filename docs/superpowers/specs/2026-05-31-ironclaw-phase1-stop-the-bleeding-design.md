# IronClaw Phase 1: 止血 — Design Spec

**Date**: 2026-05-31
**Status**: approved
**Scope**: Eliminate Setter Hell, command routing, no-op implementations, init rollback
**Non-goals**: Phase 2 (agent unification, event bus) and Phase 3 (RL cross-cutting, memory WAL)

---

## 1. Motivation

The current codebase has four structural problems that make every change harder than necessary:

1. **Setter Hell**: `Runtime` and `CognitiveAgent` have 30+ and 25+ Set* methods respectively. Every method scattered through the codebase does `if r.xxx != nil` checks. Adding a dependency means adding a Set* method, a nil guard, and a field.

2. **Command routing in handleInbound**: The 170-line `handleInbound` method in `gateway.go` has 9 if-blocks for slash commands interleaved with agent dispatch logic. Adding a command requires modifying the central dispatch function.

3. **Nil-check proliferation**: Optional dependencies (emitters, compressors, memory stores) use nil to signal "not configured." This scatters nil checks through every method that touches them, and nil pointer dereferences are the most common runtime panic.

4. **No init rollback**: `Gateway.New()` calls 15+ sequential init methods. If init #7 fails, resources from inits #1-6 leak. The 30-second init timeout has no cleanup path.

## 2. Design

### 2.1 AgentDeps — Unified Dependency Injection

**New file**: `internal/agent/deps.go`

Five grouped sub-structs, each with defined ownership:

```go
// CoreDeps — required, no no-ops. These are the minimum to run an agent.
type CoreDeps struct {
    Provider  Provider
    Tools     *tool.Registry
    Sessions  *session.Manager
    DB        *store.DB
    Cfg       config.AgentConfig
    LLMCfg    config.LLMConfig
    AgentID   string
}

// MemoryDeps — all fields default to no-op implementations via WithDefaults().
type MemoryDeps struct {
    Store          memory.Store              // noopStore{}
    LifecycleMgr   *memory.LifecycleManager  // nil = NOOP decisions
    Profiler       *memory.Profiler          // nil = no profile updates
    ContextMgr     ContextManager            // noopContextManager{}
    FactExtractor  *memory.LLMFactExtractor  // nil = no extraction
    BaseDir        string
}

// SecurityDeps — all fields default to passthrough/allow-all.
type SecurityDeps struct {
    Interceptor  Interceptor          // passthroughChain{}
    HookMgr       *hook.Manager       // nil = no hooks
    PermEngine    *tool.PermissionEngine // nil = allow-all
}

// ObservabilityDeps — all fields default to discard.
type ObservabilityDeps struct {
    Emitter        DashboardEmitter // discardEmitter{}
    MetricsEmitter MetricsEmitter   // discardMetrics{}
    ReplayRecorder *ReplayRecorder  // nil = no recording
}

// MultiAgentDeps — nil fields mean feature disabled.
type MultiAgentDeps struct {
    SkillMgr     *skill.Manager
    AgentMgr     *AgentManager
    Orchestrator *AgentOrchestrator
    SubAgentMgr  *SubAgentManager // nil = no sub-agents
    AgentMCP     *AgentMCPManager
    ResultStore  *tool.ResultStore
}

// AgentDeps is the single dependency bundle for all agent types.
type AgentDeps struct {
    Core          CoreDeps
    Memory        MemoryDeps
    Security      SecurityDeps
    Observability ObservabilityDeps
    MultiAgent    MultiAgentDeps
}
```

**Key design rules**:
- `CoreDeps` has no WithDefaults() — every field is required.
- `MemoryDeps.WithDefaults()` fills `Store` with `noopStore{}` and `ContextMgr` with `noopContextManager{}`. Pointer fields (`LifecycleMgr`, `Profiler`, `FactExtractor`) stay nil and are checked via `if != nil` only at the 3 call sites that use them (not scattered).
- `SecurityDeps.WithDefaults()` fills `Interceptor` with `passthroughChain{}` (executes tools directly, no interception).
- `ObservabilityDeps.WithDefaults()` fills `Emitter` with `discardEmitter{}` and `MetricsEmitter` with `discardMetrics{}`.
- `MultiAgentDeps` has no WithDefaults() — nil means feature disabled.

**Runtime changes**:
- Delete all 30 Set* methods.
- `NewRuntime(deps AgentDeps) *Runtime`
- `Runtime` struct keeps only: `deps AgentDeps`, `parentID string`, `depth int`, `chainID string`, `replayID string`, `bgManager *BackgroundManager`, `promptCache *PromptCache`.
- All internal methods access `r.deps.Core.Provider`, `r.deps.Memory.Store`, etc. — zero nil checks on interface fields.

**CognitiveAgent changes**:
- Delete all ~25 Set* methods.
- `NewCognitiveAgent(deps AgentDeps, opts *CognitiveAgentOptions) *CognitiveAgent`
- Removes the internal `ca.runtime = NewRuntime(...)` — instead, `CognitiveAgent` uses `deps` directly and creates a lightweight `Runtime` from the same `deps` only when it needs to delegate simple tasks.

**SubAgentManager changes**:
- `NewSubAgentManager(deps AgentDeps) *SubAgentManager`
- Removes its duplicated provider/sessions/db/tools/cfg/llmCfg fields.

**Gateway changes**:
- `initAgentRuntime()` constructs `AgentDeps` once and passes it to both `NewRuntime` and `NewCognitiveAgent`.
- All `gw.runtime.SetXxx(gw.xxx.Yyy())` calls are deleted.

### 2.2 CommandRouter — Centralized Slash Command Dispatch

**New file**: `internal/gateway/router.go`

```go
type CommandHandler func(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) (string, error)

type commandEntry struct {
    handler CommandHandler
    exact   bool // true = exact match, false = prefix match
}

// commandTable is populated in Gateway.New() after gw exists.
// Handlers are methods on *Gateway (they need gw.tools, gw.features, etc.)
// but defined in commands.go for cohesion.
var commandTable map[string]commandEntry  // populated in New()
```

**Dispatch logic**:
1. Exact match on `msg.Text` — O(1).
2. If no exact match, iterate prefix keys and check `strings.HasPrefix(msg.Text, key+" ")` or `msg.Text == key`.
3. If matched, call handler and send response.
4. If unmatched, proceed to agent dispatch.

**Handler extraction**: Each existing handler method (`handleTasksCommand`, `handleTeamCommand`, etc.) is already self-contained. They're moved from `gateway.go` to `internal/gateway/commands.go` with the new signature.

**handleInbound after refactor**: ~10 lines — rate limit check → command dispatch → agent dispatch.

### 2.3 No-op Implementations

**New files**:
- `internal/agent/noop.go` — `discardEmitter`, `discardMetrics`, `noopCompressor`
- `internal/memory/noop.go` — `noopStore`
- `internal/knowledge/noop.go` — `noopSearcher`
- `internal/tool/noop.go` — `passthroughChain` (already partially exists in interceptor.go)

**Each no-op implements the full interface** with empty or identity methods. The compiler guarantees interface satisfaction. These are defined in the same package as the interface they implement (following Go convention of `io.Discard`).

**Performance**: Empty methods on zero-size structs are inlined to nothing by the Go compiler. Zero overhead vs nil checks.

### 2.4 Init Rollback Stack

**New file**: `internal/gateway/rollback.go`

```go
type rollbackStack struct {
    cleanups []func()
}

func (r *rollbackStack) push(fn func()) {
    r.cleanups = append(r.cleanups, fn)
}

func (r *rollbackStack) run() {
    for i := len(r.cleanups) - 1; i >= 0; i-- {
        r.cleanups[i]()
    }
}
```

**Usage in Gateway.New()**:
```go
func New(cfg *config.Config) (*Gateway, error) {
    gw := &Gateway{...}
    var rb rollbackStack
    var err error
    defer func() { if err != nil { rb.run() } }()

    if err = gw.initDatabase(); err != nil { return nil, fmt.Errorf("database: %w", err) }
    rb.push(func() { gw.db.Close() })

    if err = gw.initFeatures(); err != nil { return nil, fmt.Errorf("features: %w", err) }
    // ... each init step pushes cleanup, then:
    if err = gw.initLast(); err != nil { return nil, fmt.Errorf("last: %w", err) }

    rb.cleanups = nil // success — don't rollback, Stop() handles cleanup
    return gw, nil
}
```

**Cleanup functions must be idempotent** — calling `Close()` twice must not panic. This is already true for `*store.DB.Close()` (guarded by sync.Once in the sqlite3 driver) and `context.CancelFunc` (safe to call multiple times).

## 3. File Inventory

### New files (7)
| File | Purpose |
|------|---------|
| `internal/agent/deps.go` | AgentDeps + 5 sub-structs + noopEmitter/discardMetrics/noopCompressor |
| `internal/memory/noop.go` | noopStore implementation |
| `internal/knowledge/noop.go` | noopSearcher implementation |
| `internal/tool/noop_interceptor.go` | passthroughChain implementation (extracted from interceptor.go test) |
| `internal/gateway/rollback.go` | rollbackStack |
| `internal/gateway/router.go` | commandTable + dispatch logic |
| `internal/gateway/commands.go` | extracted command handlers |

### Modified files (~15)
| File | Changes |
|------|---------|
| `internal/agent/runtime.go` | Delete Set* methods, change NewRuntime, use deps |
| `internal/agent/cognitive.go` | Delete Set* methods, change NewCognitiveAgent, use deps |
| `internal/agent/subagent.go` | Change NewSubAgentManager to accept AgentDeps |
| `internal/agent/perceive.go` | deps.Memory.Store instead of r.memStore |
| `internal/agent/reflect.go` | deps.Memory.Store instead of r.memStore |
| `internal/agent/act.go` | deps.Security.Interceptor instead of r.interceptorChain |
| `internal/agent/compression.go` | deps.Memory.ContextMgr instead of r.contextManager |
| `internal/agent/stream.go` | deps.Observability.Emitter instead of r.dashEmitter |
| `internal/gateway/gateway.go` | New() with rollbackStack, handleInbound → router |
| `internal/gateway/init_agent.go` | Construct AgentDeps, pass to NewRuntime |
| `internal/gateway/init_cognitive.go` | Pass AgentDeps to NewCognitiveAgent |
| `internal/gateway/init_multiagent.go` | Pass AgentDeps to NewSubAgentManager |
| `internal/eval/cognitive_runner.go` | Adapt test harness Agent construction |
| `internal/gateway/gateway_test.go` | Adapt test deps construction |
| `internal/agent/*_test.go` | Adapt all agent tests |

### Unchanged
- `channel/*`, `tool/*` (except noop additions), `dashboard/*`, `memory/file_store.go`, `evolution/*`, `rl/*`, `config/*`, `session/*`, `store/*`, `mcp/*`, `sandbox/*`, `scheduler/*`, `skill/*`, `taskledger/*`, `cortex/*`, `knowledge/*` (except noop), `cogmetrics/*`, `health/*`, `hook/*`, `observability/*`, `a2a/*`, `wasm/*`, `worktree/*`, `browser_agent/*`, `code_engine/*`, `collective/*`, `guardian/*`, `finetune/*`

## 4. Risk Assessment

| Risk | Likelihood | Mitigation |
|------|-----------|------------|
| Compile errors from interface changes | High | Type-safe refactor — compiler catches all mismatches |
| nil pointer panic from missed nil→noop migration | Medium | `WithDefaults()` fills interfaces; pointer fields documented at call sites |
| eval harness breaks | Medium | `cognitive_runner.go` is the only eval file touching agent construction |
| Test mock construction too verbose | Low | Gateway tests already construct minimal deps; no-op defaults reduce mock boilerplate |
| CognitiveAgent losing internal Runtime breaks simple-task delegation | Low | Runtime is lightweight — creating it from the same deps on-demand is trivial |

## 5. Success Criteria

1. Zero `Set*` methods on `Runtime`, `CognitiveAgent`, or `SubAgentManager`
2. Zero `if r.xxx != nil` checks on interface-typed fields in agent methods (pointer fields like `*LifecycleManager` may still be nil-checked at their 3 call sites)
3. `handleInbound` is under 20 lines
4. `Gateway.New()` uses rollbackStack — any init failure triggers cleanup
5. All existing tests pass
6. `go build ./...` succeeds

## 6. Phase 2 Preview (not in scope)

Phase 1 is the foundation. Phase 2 will build on it:
- Unified `Agent` type with `LoopStrategy` interface (replaces separate Runtime/CognitiveAgent)
- Event bus generalization (replaces manual emitter passing)
- CognitiveAgent embeds Runtime via shared deps rather than duplicating

---

*End of Phase 1 design spec.*
