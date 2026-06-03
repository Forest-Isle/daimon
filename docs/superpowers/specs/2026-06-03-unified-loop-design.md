# Unified Loop + Planning-as-Tool: Architecture Spec

**Date**: 2026-06-03  
**Status**: Approved → Implementation  
**Goal**: Replace 2-mode agent architecture (Simple + Cognitive 5-phase) with a single UnifiedLoop where LLM autonomously decides when to decompose tasks via the `plan_task` tool.

---

## 1. Motivation

### Current State
```
Agent.HandleMessage
  ├─ mode=simple  → SimpleLoop (267 lines, correct)
  └─ mode=cognitive → CognitiveLoop (1014+2000 lines orchestration)
                        ├─ Perceiver (443 lines, 15 Set* methods)
                        ├─ Planner   (347 lines, 8 Set* methods, mandatory LLM call)
                        ├─ Executor  (664 lines, DAG scheduling — valuable!)
                        ├─ Observer  (155 lines, bookkeeping)
                        └─ Reflector (546 lines, 8 Set* methods, mandatory LLM call)
```

### Problems
1. **Dual-mode complexity**: complexity classifier routes "read README.md" into 5-phase cognitive loop
2. **3 LLM calls per iteration** (PLAN + ACT stream + REFLECT) vs 1 in simple loop
3. **Phase-ism**: Context gathering and bookkeeping dressed as cognitive stages with dedicated structs
4. **LLM-as-architect**: Code decides when to plan; LLM should decide
5. **~3,200 LOC** of phase orchestration with zero dedicated tests

### Industry Alignment
Claude Code paper: 98.4% deterministic infrastructure, 1.6% AI logic. Core is a simple while-loop. LLM autonomously uses TodoWrite tool for task decomposition — planning is a tool, not a phase.

---

## 2. Target Architecture

```
Agent.HandleMessage
  └─ UnifiedLoop (sole LoopStrategy)
       ├─ assembleContext() — deterministic context assembly
       ├─ LLM stream + speculative execution
       ├─ dispatchToolsParallel() — concurrent tool dispatch
       │    ├─ bash, read, write, edit, grep... (ordinary tools)
       │    ├─ plan_task (LLM chooses to call → internal DAG execution)
       │    └─ spawn_subagent (existing, unchanged)
       └─ repeat until LLM says stop
```

### Key Design Decisions

1. **Single loop strategy** — no mode switch, no complexity classifier, no `LoopStrategy` interface
2. **Planning is a tool** — `plan_task` tool wraps extracted DAG executor; LLM calls it when needed
3. **Parallel tool dispatch** — multiple independent tool_use blocks from a single LLM response execute concurrently
4. **No REFLECT** — LLM sees results and naturally decides next steps (repair, skip, summarize)
5. **No PERCEIVE/OBSERVE ceremony** — context assembly is deterministic prep; observation is inline in results

---

## 3. Component Design

### 3.1 DAG Executor (`internal/dag/`)

**New package. Zero external dependencies.**

```go
package dag

type Task struct {
    ID, Description, ToolName, ToolInput string
    DependsOn []string
}

type Result struct {
    TaskID, Output, Error string
    DurationMs int64
    Status     Status // done | failed | skipped
}

type ExecuteFunc func(ctx context.Context, t Task) Result

func Execute(ctx context.Context, tasks []Task, exec ExecuteFunc, maxParallel int) []Result
```

Extracted from `act.go` Executor.RunWithContext — worker pool + channel + semaphore, topological readiness, failure propagation. ~150 LOC. Independently testable.

**What stays in act.go**: `executeToolCall` wrapper (hook → permission → interceptor → actual execution) remains in agent package. The dag package only calls back through ExecuteFunc.

### 3.2 plan_task Tool (`internal/tool/plan_task.go`)

```go
type PlanTaskTool struct {
    tools       *Registry
    maxParallel int
    db          *store.DB
    hookMgr     *hook.Manager
    permEngine  *PermissionEngine
    interceptor *InterceptorChain
}
```

**Execute flow**: parse LLM's JSON plan → convert to dag.Tasks → construct ExecuteFunc wrapping single-tool execution → call dag.Execute() → return structured results.

**System prompt registration**: tool with `subtasks[]` parameter, each with id/description/tool_name/tool_input/depends_on/confidence.

### 3.3 UnifiedLoop (`internal/agent/unified_loop.go`)

Replaces `simple_loop.go` + `cognitive_loop.go`. ~350 LOC.

**Key changes from SimpleLoop**:
- `assembleContext()` merged from PERCEIVE: memory/knowledge/project/git context injection
- `dispatchToolsParallel()`: concurrent execution of independent tool_use blocks
- No mode branching — single execution path

### 3.4 Compression (5 layers → 3)

| Before | After |
|--------|-------|
| Layer 0: tool_output_prune | Layer 0: tool_output_reduce (merged 0+1) |
| Layer 1: tool_eviction | — |
| Layer 2: turn_summarization | Layer 1: turn_summarization |
| Layer 3: old_context_removal | Layer 2: emergency_truncation (merged 3+4) |
| Layer 4: emergency_truncation | — |

~1200 LOC → ~500 LOC. Remove `ensureToolPairing()` band-aid by making layers pairing-aware.

---

## 4. Deletion Manifest

| File | LOC | Reason |
|------|-----|--------|
| `agent/cognitive_loop.go` | 1014 | Replaced by UnifiedLoop |
| `agent/perceive.go` | 443 | Context assembly inlined |
| `agent/plan.go` | 347 | Replaced by plan_task tool |
| `agent/observe.go` | 155 | Bookkeeping inlined in results |
| `agent/reflect.go` | 546 | LLM naturally reflects; no code needed |
| `agent/loop_strategy.go` | 14 | Single strategy, no interface |
| `agent/cognitive_types.go` | 243 | Replaced by slim types.go |
| `agent/assertion.go` | ~200 | Now part of plan_task DAG internals |
| `agent/failure_context.go` | ~150 | No REFLECT, no failure context needed |
| `agent/checkpoint.go` | ~100 | Cognitive checkpoint, no longer needed |
| `agent/context_budget.go` | ~70 | Hardcoded switch, dead code |
| `agent/tool_cache.go` | ~50 | Overlaps with speculative executor |
| `gateway/init_cognitive.go` | ~150 | Cognitive wiring removed |
| `gateway/subsystem_evolution.go` | ~70 | Evolution subsystem wrapper |

**Total deleted**: ~3,500 LOC

### New Files

| File | LOC |
|------|-----|
| `dag/executor.go` | ~150 |
| `dag/executor_test.go` | ~100 |
| `tool/plan_task.go` | ~250 |
| `tool/plan_task_test.go` | ~150 |
| `agent/unified_loop.go` | ~350 |
| `agent/types.go` (slim) | ~60 |

**Net reduction**: ~3,200 LOC

---

## 5. Gateway Wiring (After)

```go
func (gw *Gateway) initAgentRuntime() error {
    // LLM provider (unchanged)
    // AgentDeps (simplified: no cognitive/evolution fields)
    
    // Single loop strategy — always UnifiedLoop
    gw.agent = agent.NewAgent(deps.WithDefaults(), &agent.UnifiedLoop{}, agent.NewEventBus())
    
    // Register plan_task tool
    planTaskTool := tool.NewPlanTaskTool(gw.tools, cfg.MaxParallel, gw.db, ...)
    gw.tools.Register(planTaskTool)
}
```

Removed: `init_cognitive.go`, evolution wiring, `LoopStrategy` interface registration.

---

## 6. Implementation Order

| Step | Description | Risk | Deps |
|------|-------------|------|------|
| 1 | Create `internal/dag/` — DAG executor + tests | Low | None |
| 2 | Create `internal/tool/plan_task.go` — plan_task tool + tests | Low | Step 1 |
| 3 | Create `internal/agent/unified_loop.go` — parallel dispatch + context assembly | Medium | Step 2 |
| 4 | Gateway: register plan_task, wire UnifiedLoop | Medium | Step 3 |
| 5 | Compression 5→3 layers | Low | None |
| 6 | Delete cognitive loop files | Medium | Step 4 verified |
| 7 | Cleanup: imports, dead types, `command_feature.go` | Low | Step 6 |
| 8 | Integration tests with mock LLM | Medium | Step 7 |

---

## 7. Risk Mitigation

- **Backward compat**: Config flag `agent.mode: unified` (default) with `agent.mode: cognitive` fallback for transition period. Remove fallback once verified.
- **LLM compatibility**: plan_task tool is offered to LLM like any other tool. If LLM never calls it, behavior is identical to current SimpleLoop.
- **Testing**: Mock LLM provider returns pre-scripted tool_use blocks. Verify plan_task DAG execution and parallel dispatch independently.

---

## 8. Non-Goals (Out of Scope)

- Memory system simplification (SQLite-only) — separate spec
- Evolution package removal — separate spec
- Dashboard removal — separate spec
- Knowledge graph removal — separate spec
- Sandbox/interceptor simplification — separate spec
