# IronClaw Codebase Optimization — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce codebase from 64,798 production LOC to <48,000 LOC across 6 phases, eliminating dead code, consolidating overlapping systems, and decomposing the agent monolith.

**Architecture:** Phase-gated sequential execution. Each phase removes or reorganizes code with escalating risk. Phase 1 is pure dead-code deletion (zero risk). Phases 2-3 restructure the agent package. Phases 4-6 consolidate memory, gateway, eval, and tool subsystems.

**Tech Stack:** Go 1.23+, CGO_ENABLED=1, -tags fts5, SQLite, Docker (sandbox)

---

## Phase 1: Safe Removals — Dead Code Deletion (Zero Risk)

### Task 1.1: Remove orphaned packages

**Files to DELETE:**
- `internal/wasm/` (8 files, 693 LOC) — zero imports, zero callers
- `internal/collective/` (8 files, 875 LOC) — zero imports, zero callers
- `internal/a2a/` (4 files, 944 LOC) — needs gateway cleanup
- `internal/code_engine/` (6 files, 897 LOC) — zero imports, zero callers

**Files to MODIFY:**
- `internal/gateway/gateway.go` — remove wasm/a2a imports, fields, init/cleanup
- `internal/gateway/features.go` — remove wasm_plugins, a2a feature entries

**Gateway struct fields to remove:**
- Line 59: `wasmHost *wasm.PluginHost`
- Line 68: `replayRecorder *agent.ReplayRecorder`
- Line 69: `selfHealEngine *agent.SelfHealEngine`
- Line 70: `treePlanner *agent.StrategicTreePlanner`
- Line 71: `mctsPlanner *agent.MCTSPlanner`
- Line 86: `a2a *A2ASubsystem`

**Gateway methods/init to remove:**
- Line 131: `gw.a2a = &A2ASubsystem{}`
- Lines 221-222: `initGraphEngine()` call
- Lines 235-248: wasm plugin host init block
- Lines 491-493: wasmHost.Close()
- Lines 977-1046: entire a2a initA2A/stopA2A methods
- Lines 1050+: loadWasmPlugins method

**Features to remove (features.go):**
- Lines 145-148: wasm_plugins feature entry
- Lines 153-156: a2a feature entry (check exact lines)
- Lines 230-238: wasm_plugins lifecycle hooks
- Lines 240-248: a2a lifecycle hooks

### Task 1.2: Remove redundant agent planning/loop files

**Files to DELETE (in internal/agent/):**
- `mcts_planner.go` (729 lines)
- `tree_planner.go` (449 lines)
- `autonomous_loop.go` (543 lines)
- `self_heal.go` (552 lines)
- `debate.go` (190 lines)
- `cognitive_debate.go` (87 lines)
- `graph_engine.go` (170 lines)
- `graph_nodes.go` (149 lines)
- `graph_node_adapters.go` (381 lines)
- `graph_types.go` (119 lines)

**Gateway files to MODIFY:**
- `internal/gateway/init_agent.go` — remove ReplayRecorder from agent opts
- `internal/gateway/init_cognitive.go` — remove treePlanner assignment, DebateConfig
- `internal/gateway/init_graph.go` — DELETE entire file
- `internal/gateway/init_multiagent.go` — remove sidechain store init, SetDebateConfig

### Task 1.3: Remove streaming wrapper files

**Files to DELETE (in internal/agent/):**
- `streaming_pipeline.go` (240 lines)
- `streaming_execute.go` (141 lines)
- `streaming_observe.go` (106 lines)
- `streaming_perceive.go` (89 lines)
- `streaming_plan.go` (225 lines)
- `streaming_reflect.go` (129 lines)

### Task 1.4: Remove replay and misc agent files

**Files to DELETE (in internal/agent/):**
- `replay.go` (283 lines)
- `replay_engine.go` (167 lines)
- `replay_sqlite.go` (172 lines)
- `sidechain.go` (297 lines)
- `aggregator.go` (70 lines)

### Task 1.5: Fix compilation

**Commands:**
```bash
# After all deletions, fix any remaining import errors
go build -tags fts5 ./cmd/ironclaw/
# Fix any feature registry references
# Run tests
CGO_ENABLED=1 go test -tags fts5 ./internal/agent/... ./internal/gateway/...
```

**Gate:** All tests pass. `go build` succeeds. No import errors.

**Estimated LOC removed:** ~8,000

---

## Phase 2: Context Consolidation & Config Cleanup

*(Deferred — implement after Phase 1 gates pass)*

### Task 2.1: Consolidate context management

Merge 6 files into 3:
- `compaction.go` → merge into `context_manager.go`
- `context_budget.go` → merge into `compression.go`
- Keep: `context_manager.go`, `compression.go`, `context_builder.go`, `context.go`

### Task 2.2: Simplify config

- Remove hierarchy merging from config
- Remove wasm, a2a, collective, code_engine, graph, reranker config sections from ironclaw.example.yaml
- Update config structs in internal/config/

---

## Phase 3-6: (Deferred — see spec for full details)

- Phase 3: Agent package split into 5 subpackages
- Phase 4: Memory/knowledge/cortex consolidation
- Phase 5: Gateway decomposition + eval downsizing
- Phase 6: Tool consolidation + config simplification
