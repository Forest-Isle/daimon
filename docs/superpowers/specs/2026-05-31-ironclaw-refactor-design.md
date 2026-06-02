# IronClaw Refactor — Architecture & Implementation Design

**Date:** 2026-05-31
**Status:** Approved
**Scope:** P0 fixes → Cognitive rewrite → Gateway split → Tests → Config → TUI

---

## 1. Problem Summary

IronClaw at 107k lines / 556 Go files has accumulated technical debt from rapid L0-L9 feature
iteration. The core issues:

| Category | Symptom | Root Cause |
|----------|---------|------------|
| God Object | gateway.go: 1201 lines, 55+ fields | No subsystem abstraction |
| Monolith | cognitive.go: 1562 lines, 30 optional deps | 5-phase loop hardcoded, phases are structs not interfaces |
| Dead Code | core/ package (2.5k lines) unused | Rewrite abandoned mid-migration |
| Zero Tests | 9 packages, 25+ source files | Speed prioritized over safety |
| Bugs | context leak, residual panic | Missed in cleanup pass |
| Config Bloat | 1565 lines config code | Hierarchy overengineered |

---

## 2. Design Philosophy

**Single LLM Tool-Use Loop + Self-Correction Hook.**

The 5-phase cognitive model (PERCEIVE→PLAN→ACT→OBSERVE→REFLECT) was designed for the
GPT-3.5 era when models needed explicit guidance at each step. Modern LLMs (Claude 4,
GPT-4) complete perceive+plan implicitly in their thinking. Retaining 5 explicit phases
adds latency (3-5 LLM calls/cycle) without proportionate value.

What survives from the old model:
- **Context injection** — one-time, pre-loop: project scan + git + memory + knowledge
- **Tool-use loop** — the core: LLM(tools+ctx+history) → execute → results → repeat
- **Self-correction** — post-loop hook: assertion check → failure context injection → rerun

All cross-cutting concerns (checkpoint, replay, plan-mode, compression, self-heal) become
middleware on the tool-use loop.

---

## 3. New Cognitive Architecture

### 3.1 Core Loop

```
CognitiveAgent.Run(ctx, task) → Result

  1. CONTEXT BUILD (once)
     ProjectScanner + GitProvider + MemorySearch + KBRetrieval
     → populate system prompt {{DYNAMIC_CONTEXT}}

  2. TOOL-USE LOOP (until done or max_iterations)
     ┌─────────────────────────────────┐
     │ LLM.Complete(messages + tools)  │
     │   → assistant message           │
     │   → if tool_calls:              │
     │       execute via middleware     │
     │       append results            │
     │       continue loop             │
     │   → if text response:           │
     │       DONE                      │
     └─────────────────────────────────┘

  3. SELF-CORRECTION HOOK (post-loop)
     ┌─────────────────────────────────┐
     │ AssertionEngine.Verify(result)  │
     │   → PASS: extract learnings     │
     │          save memory            │
     │   → FAIL: inject failure ctx    │
     │           rerun loop (max N)    │
     └─────────────────────────────────┘
```

### 3.2 Middleware Chain

Tool execution wraps through an onion-model middleware chain:

```go
type ToolMiddleware interface {
    Wrap(next ToolExecutor) ToolExecutor
}

type ToolExecutor func(ctx context.Context, call ToolCall) (*ToolResult, error)
```

Built-in middleware:
- `HookMiddleware` — pre/post tool hooks
- `PermissionMiddleware` — approval flow
- `SandboxMiddleware` — Docker/file/network isolation
- `SpeculativeMiddleware` — read-only tool pre-execution
- `ReplayMiddleware` — record all tool calls
- `SelfHealMiddleware` — auto-retry on tool errors

### 3.3 Pipeline Hooks (Loop-Level)

Cross-cutting concerns that wrap the entire loop:

```go
type LoopHook interface {
    BeforeLoop(ctx context.Context, state *LoopState) error
    AfterTurn(ctx context.Context, state *LoopState) error
    AfterLoop(ctx context.Context, result *LoopResult) error
}
```

Built-in hooks:
- `CheckpointHook` — save/restore loop state
- `PlanModeHook` — pause before tool execution, wait for approval
- `CompressionHook` — reactive 413 context compression
- `MetricsHook` — OTel tracing + Prometheus metrics
- `EvolutionHook` — dispatch events to evolution engine

### 3.4 Context Builder (Replaces PERCEIVE)

```go
type ContextBuilder struct {
    scanners []ContextScanner
}

type ContextScanner interface {
    Scan(ctx context.Context) (*ContextFragment, error)
}

// Scanners:
// - ProjectContextScanner  (file tree, language detection)
// - GitContextScanner      (branch, status, recent log)
// - MemoryContextScanner   (relevant memories via hybrid search)
// - KnowledgeContextScanner (KB + knowledge graph)
// - ProfileContextScanner  (user profile sections)
```

Run once before loop. Results merged into `{{DYNAMIC_CONTENT}}` template in system prompt.

### 3.5 Self-Correction Engine (Replaces OBSERVE+REFLECT)

```go
type SelfCorrectionEngine struct {
    assertionEngine *AssertionEngine
    maxRetries      int
}

func (e *SelfCorrectionEngine) VerifyAndCorrect(
    ctx context.Context,
    loopResult *LoopResult,
    runner func(ctx context.Context, extraContext string) (*LoopResult, error),
) (*LoopResult, error) {
    for attempt := 0; attempt <= e.maxRetries; attempt++ {
        assertions := e.assertionEngine.Verify(loopResult)
        if len(assertions.Failures) == 0 {
            return loopResult, nil
        }
        failureCtx := e.buildFailureContext(assertions.Failures)
        loopResult, _ = runner(ctx, failureCtx)
    }
    return loopResult, nil
}
```

---

## 4. File Structure Changes

### New Files

```
internal/agent/
  cognitive_v2.go          — new CognitiveAgent (Run loop)
  context_builder.go       — ContextBuilder + scanners
  self_correction.go       — SelfCorrectionEngine
  middleware_tool.go       — ToolMiddleware chain
  middleware_loop.go       — LoopHook chain
  cognitive_v2_test.go     — integration tests

internal/agent/phase/       — old phase implementations, preserved
  perceive.go → context_scanner_project.go
  context_scanner_git.go
  context_scanner_memory.go
  act.go         → executor.go (tool dispatch logic)
  observe.go     → assertion_engine.go
  reflect.go     → (absorbed into self_correction.go)
```

### Modified Files

```
internal/gateway/gateway.go     — wire new CognitiveAgent, remove 15+ fields
internal/gateway/init_agent.go  — build middleware chain
internal/agent/cognitive.go     — deprecation comment, Run() delegates to cognitive_v2.Run()
```

### Transition Strategy (No Config Flag)

Old `cognitive.go` is NOT deleted. Its `Run()` method becomes a thin wrapper:

```go
// Run executes the cognitive loop.
// Deprecated: CognitiveAgent is replaced by the v2 single-loop architecture.
// This method delegates to CognitiveAgentV2 internally.
func (ca *CognitiveAgent) Run(ctx context.Context, ...) (*RunResult, error) {
    v2 := ca.toV2()
    return v2.Run(ctx, ...)
}
```

This ensures all existing callers (gateway, eval harness, channels) continue to compile
and function without changes. The v2 implementation is a drop-in replacement at the
`Run()` interface level. If any behavior diverges, the old implementation is preserved
in cognitive.go as reference.

### Deleted/Archived

```
internal/core/                  → internal/archived/core/
internal/core/adapter/         → internal/archived/core/adapter/
internal/core/boot/            → internal/archived/core/boot/
```

---

## 5. Gateway Subsystem Split

Extract 8 subsystems from the 55-field Gateway:

```go
type Subsystem interface {
    Name() string
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
}
```

### Subsystem Breakdown

| Subsystem | Extracted Fields | Hot-Reload |
|-----------|-----------------|------------|
| `MemorySubsystem` | memStore, embedder, factExtractor, lifecycleMgr, consolidator, compactor, profiler | No |
| `ChannelSubsystem` | channels, sched | Yes (channels) |
| `DashboardSubsystem` | dashboardBus, dashboardHub, dashboardSrv, stateTracker, dashEmitter | Yes |
| `SandboxSubsystem` | dockerSessionMgr, interceptorChain | Yes |
| `EvolutionSubsystem` | evoEngine, cogCollector, healthChecker, breaker | Yes |
| `TaskSubsystem` | taskLedger, teamCoordinator, subAgentMgr, teamManager, staleDetector | No |
| `ObservabilitySubsystem` | obsShutdown, rateLimiter | Yes |
| `A2ASubsystem` | a2aServer | Yes |

Gateway retains: cfg, db, sessions, provider, cognitiveAgent, tools, features (core orchestration).

### Before/After

```
Before: type Gateway struct {   // 55 fields, 1201 lines
After:  type Gateway struct {   // ~18 fields, ~400 lines
            cfg, db, sessions, provider
            cognitiveAgent *agent.CognitiveAgent
            tools          *tool.Registry
            hookMgr        *hook.Manager
            features       *feature.Registry
            subsystems     []Subsystem  // 8 subsystems
            ...
        }
```

---

## 6. P0 Bug Fixes

### 6.1 Context Leak (gateway.go:132)
```go
// BEFORE:
ctx, cancel := context.WithTimeout(ctx, 30*time.Second)

// AFTER:
ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
defer cancel()
```

### 6.2 Residual Panic (core/tools.go:80)
```go
// BEFORE:
if err != nil {
    panic(err.Error())
}

// AFTER:
if err != nil {
    return nil, fmt.Errorf("tool registry init: %w", err)
}
```
Note: This file will be archived with core/. Fix applied before archival for safety.

### 6.3 Gitignore
Add to `.gitignore`: `web/studio/node_modules/`

---

## 7. Test Completion Strategy

### 7.1 Level A — Integration Tests

**knowledge/** (7 source files + graph/ 14 files)
- Strategy: Real SQLite with FTS5, full ingest→chunk→embed→search pipeline
- Files: `store_test.go`, `graph/graph_test.go`, `pipeline_test.go`
- ~800 lines

**wasm/** (6 source files)
- Strategy: Compile minimal .wasm test module, verify load→call→sandbox
- Files: `plugin_test.go`, `host_test.go`
- ~400 lines

**code_engine/** (3 source files)
- Strategy: Mock filesystem with sample Go source, verify index→query→callgraph
- Files: `symbol_index_test.go`, `search_test.go`
- ~500 lines

### 7.2 Level B — Unit Tests

**guardian/** — mock checkers, verify pipeline execution. ~250 lines
**browser_agent/** — mock HTTP/Playwright, verify action orchestration. ~200 lines
**finetune/** — verify JSONL output, token counting. ~150 lines

### 7.3 Level C — Smoke Tests

**channel/** — interface compilation + key function table tests. ~80 lines
**userdir/** — path resolution table tests. ~50 lines
**util/** — TruncateStr + helpers table tests. ~50 lines

### 7.4 Verification

All tests must pass: `CGO_ENABLED=1 go test -tags fts5 ./internal/...`

---

## 8. Config Simplification

- Extract `config/merge.go` — merge logic from hierarchy.go
- Extract `config/validate.go` — validation rules
- Remove duplicate `StreamingEnabled` field (keep in config_agent.go, remove default in config.go:101)
- Annotate deprecated fields with `// Deprecated:` comments + migration notes
- Target: reduce from 1565 lines to ~1100 lines

---

## 9. TUI Model Split

Split `internal/channel/tui/model.go` (1111 lines):

```
model.go          — Bubble Tea Model struct + Init (core)
model_update.go   — Update() method + message routing (~400 lines)
model_view.go     — View() method + rendering (~400 lines)
model_dialogs.go  — dialog/window management (~200 lines)
model_stats.go    — stats panel + compression notifications (~100 lines)
```

---

## 10. Implementation Order

### Worktree 1: P0 Fixes + Core Archive
- Context leak fix
- Panic fix  
- Gitignore
- Archive core/ → internal/archived/core/
- **Merge first** (zero conflict with anything else)

### Worktree 2: Cognitive Rewrite (The Big One)
- `cognitive_v2.go` — new Run loop
- `context_builder.go` — context injection
- `self_correction.go` — post-loop correction
- `middleware_tool.go` — tool middleware chain
- `middleware_loop.go` — loop hooks
- Refactor existing phase files into new structure
- Update gateway.go to wire new agent
- **Merge after Worktree 1**

### Worktree 3: Gateway Subsystem Split
- Define Subsystem interface
- Extract 8 subsystems
- Wire in gateway.go
- **Merge after Worktree 2** (depends on agent structure stable)

### Worktree 4: Test Completion
- Level A → B → C tests
- All independent of code changes
- **Merge anytime** (no code conflicts with 1-3)

### Worktree 5: Config + TUI Cleanup
- Config simplification
- TUI model split
- **Merge last** (cosmetic, low risk)

---

## 11. Risk Assessment

| Risk | Probability | Impact | Mitigation |
|------|------------|--------|------------|
| Cognitive rewrite breaks existing behavior | Medium | High | Feature flag switch; parallel old/new until validated |
| Eval harness breaks | High | Medium | Update eval fixtures as part of Worktree 2 |
| Channel adapters (TUI/Telegram/Discord) break | Medium | High | Each channel tested manually before merge |
| Test completion takes longer than expected | Medium | Low | Level A first (most value), B+C can follow |
| Merge conflicts between worktrees | Low | Medium | Clear dependency order; Worktree 4 merges anytime |

---

## 12. Success Criteria

1. `go vet ./internal/...` passes with zero warnings
2. `CGO_ENABLED=1 go test -tags fts5 ./internal/...` passes
3. Gateway struct has ≤ 20 fields (down from 55)
4. Cognitive agent code ≤ 2000 lines total (down from ~3500)
5. Zero packages with source code and no tests
6. Zero remaining `panic()` calls outside of `main`/`init`
7. `internal/core/` directory no longer exists
8. Agent passes existing eval suite (`make eval-baseline`)
