# Agent Package Decomposition Design

Date: 2026-05-26 | Status: Design | Author: ENI & LO

## Overview

Decompose `internal/agent/` from a monolithic 23,621-line, 50+ file package into 1 orchestration layer + 11 domain sub-packages. Each sub-package has a single responsibility, zero cross-dependencies, and is independently testable.

## Motivation

### Current Problems

1. **Package bloat.** 50+ files in one directory. `cognitive.go` alone is 990 lines. No one can hold the whole package in their head.
2. **Implicit coupling.** Everything shares the same namespace. Private functions bleed across files (e.g., `generateAssertions` is called from `act.go` but defined in `assertion.go`). Renaming or refactoring one subsystem risks breaking distant, seemingly unrelated code.
3. **Test isolation is poor.** All tests run in `package agent`. They share test helpers, fixtures, and sometimes accidentally depend on each other's side effects.
4. **RL/Evolution removal left dead code paths.** `act.go` still has commented-out RL hook points. The deletion of `internal/evolution/` and `internal/rl/` was done at the directory level but the agent package never got cleaned up internally.
5. **New modules feel bolted on.** `auto_heal.go` (624 lines), `planner_tree.go` (582 lines), `recorder.go` + `replayer.go` (430 lines) were added as new files in the flat package. They're self-contained but structurally invisible — you can't tell from the file tree what the agent's capabilities are.

### Design Goals

- **Single responsibility per package.** Each sub-package answers "what does this do?" in one sentence.
- **Zero cross-dependencies between sub-packages.** planning/ never imports execution/. The top-level agent/ is the sole orchestrator.
- **Independently testable.** Each sub-package's tests run in isolation with no knowledge of other sub-packages.
- **Minimal interface surface.** Only define Go interfaces where multiple implementations exist or are planned.
- **Backward compatible externally.** `internal/gateway/`, `cmd/`, `internal/eval/` consumers see the same public API. Import path changes are mechanical.

## Target Architecture

### Layer Model

```
Layer 0 (external, unchanged): tool/, store/, config/, session/, memory/, channel/, hook/, knowledge/
Layer 1 (domain sub-packages, 11 total): planning/ execution/ healing/ perception/ observation/
                                          reflection/ recording/ runtime/ provider/ subagent/ checkpoint/
Layer 2 (orchestration): agent/ — CognitiveAgent loop + wiring
```

**Hard rule:** Layer 1 packages may only import from Layer 0. Never from each other. Never from Layer 2.

### Package Specifications

#### `internal/agent/provider/` — LLM Provider Interface

**Files:** `provider.go` (interface), `claude.go`, `openai.go`, `retry.go`

**Exports:**
```
type Provider interface {
    Complete(ctx, req) → Response
    Stream(ctx, req) → Stream
}
type CompletionRequest struct { ... }
type CompletionResponse struct { ... }
type ClaudeProvider struct { ... }
type OpenAIProvider struct { ... }
type RetryProvider struct { ... }
```

**Dependencies:** `anthropics-sdk-go`, `net/http` (zero IronClaw internal deps)

**Why separate:** Provider is the only package with 3 implementations (Claude, OpenAI, Retry). It's the natural place for the `Provider` interface. Moving it out breaks no cycles — it's already a leaf.

---

#### `internal/agent/observation/` — Tool Output Assertions

**Files:** `observation.go` (types), `assertion.go` (engine)

**Exports:**
```
type Observation struct { ... }
type AssertionResult struct { ... }
type Observer struct { ... }
func GenerateAssertions(obs Observation) []AssertionResult
func (o *Observer) Observe(results []Observation) []AssertionResult
```

**Dependencies:** None (pure logic — string matching, JSON validation, structural checks on tool outputs)

**Why separate:** Assertions are the most self-contained subsystem. Zero imports. Moving it first proves the decomposition pattern works with minimal risk.

---

#### `internal/agent/recording/` — Session Trajectory Recording & Replay

**Files:** `recorder.go`, `replayer.go`

**Exports:**
```
type SessionRecorder struct { ... }
type SessionReplayer struct { ... }
type RecordingEvent struct { ... }
type SessionDiff struct { ... }
type ReplayHandler func(RecordingEvent) error

func NewSessionRecorder(db *store.DB) *SessionRecorder
func (r *SessionRecorder) RecordEvent(ctx, event) error
func (r *SessionRecorder) GetSessionEvents(sessionID) ([]RecordingEvent, error)
func (r *SessionRecorder) ListSessions(limit) ([]SessionSummary, error)
func (r *SessionReplayer) ReplaySession(ctx, sessionID, handler) error
func (r *SessionReplayer) DiffSessions(a, b) (*SessionDiff, error)
```

**Dependencies:** `store/` (DB handle only)

---

#### `internal/agent/healing/` — Automatic Error Recovery

**Files:** `healer.go` (AutoHealer + fix strategies), `patterns.go` (error patterns)

**Exports:**
```
type AutoHealer struct { ... }
type AutoHealResult struct { ... }
type AutoHealFix struct { ... }
type AutoHealContext struct { ... }

func (ah *AutoHealer) Diagnose(obs, failures) (*AutoHealResult, error)
func (ah *AutoHealer) ApplyFix(ctx, diagnosis, executor) (*AutoHealResult, error)
func (ah *AutoHealer) Heal(ctx, obs, failures, executor) (*AutoHealResult, error)
func (ah *AutoHealer) AttemptFix(ctx, ahCtx) (*AutoHealFix, error)
```

**Note on ToolExecutor:** healing/ needs to re-execute corrected tool calls. It imports `tool.ToolCall` for the type but does NOT import `execution/`. Instead, `execution/` defines `type ToolExecutor interface { ExecuteTool(ctx, call) (output, errMsg) }` and injects itself. healing/ accepts the interface.

**Dependencies:** `tool/` (ToolCall type only)

---

#### `internal/agent/checkpoint/` — Task Checkpoint Persistence

**Files:** `checkpoint.go`

**Exports:**
```
type CheckpointStore struct { ... }
type Checkpoint struct { ... }

func NewSQLiteCheckpointStore(db *store.DB) *CheckpointStore
func (cs *CheckpointStore) Save(sessionID, state) error
func (cs *CheckpointStore) Load(sessionID) (*Checkpoint, error)
func (cs *CheckpointStore) Delete(sessionID) error
```

**Dependencies:** `store/` (DB handle only)

---

#### `internal/agent/planning/` — Task Planning & MCTS Search

**Files:** `plan.go` (Planner + TaskPlan/SubTask types), `tree.go` (TreePlanner MCTS)

**Exports:**
```
type TaskPlan struct { ... }
type SubTask struct { ... }
type Planner struct { ... }
type TreePlanner struct { ... }
type PlanTreeNode struct { ... }

func NewPlanner(provider, tools, cfg, model) *Planner
func (p *Planner) Generate(ctx, state) (*TaskPlan, error)
func NewTreePlanner(provider, depth, branching, exploration) *TreePlanner
func (tp *TreePlanner) Search(ctx, state, initialPlan) (*PlanTreeNode, error)
```

**Dependencies:** `provider/`, `config/`, `tool/`

---

#### `internal/agent/perception/` — Context Gathering

**Files:** `perceive.go` (Perceiver + CognitiveState), `project_scanner.go`, `git_context.go`, `context_budget.go`, `failure_context.go`

**Exports:**
```
type CognitiveState struct { ... }
type Perceiver struct { ... }
type ProjectContextScanner struct { ... }
type GitContextProvider struct { ... }
type ContextBudgetAllocator struct { ... }

func NewPerceiver(memStore, memBaseDir) *Perceiver
func (p *Perceiver) Perceive(ctx, userMsg) (CognitiveState, error)
func (p *Perceiver) SetKnowledgeSearcher(s)
func (p *Perceiver) SetKnowledgeGraph(g)
```

**Dependencies:** `memory/`, `knowledge/`, `knowledge/graph/`

---

#### `internal/agent/reflection/` — Post-Action Reflection

**Files:** `reflect.go`

**Exports:**
```
type Reflector struct { ... }
type ReflectionResult struct { ... }

func NewReflector(provider, memStore, cfg, model) *Reflector
func (r *Reflector) Reflect(ctx, obs, state) (*ReflectionResult, error)
```

**Dependencies:** `provider/`, `memory/`

---

#### `internal/agent/execution/` — Tool Dispatch & Execution

**Files:** `executor.go` (Executor), `concurrent.go` (parallel dispatch), `tool_cache.go`

**Exports:**
```
type Executor struct { ... }
type ToolExecutor interface {
    ExecuteTool(ctx, call) (output string, errMsg string)
}

func NewExecutor(tools, db, approvalFunc, cfg) *Executor
func (e *Executor) Run(ctx, ch, sess, plan) ([]Observation, error)
func (e *Executor) RunWithContext(ctx, ch, sess, plan, taskCtx) ([]Observation, error)
func (e *Executor) SetInterceptorChain(chain)
func (e *Executor) SetHookManager(mgr)
func (e *Executor) SetPermissionEngine(engine)
func (e *Executor) SetAutoHealer(healer)           // injected from healing/
```

**Dependencies:** `tool/`, `store/`, `session/`, `hook/`, `channel/`, `observation/`, `healing/` (via interface injection, not import)

---

#### `internal/agent/runtime/` — Simple Agent Mode

**Files:** `runtime.go` (Runtime), `stream.go`, `compression.go`, `context_manager.go`, `speculative.go`, `sidechain.go`

**Exports:**
```
type Runtime struct { ... }
type ContextManager interface { ... }
type PipelineContextManager struct { ... }

func NewRuntime(provider, tools, sessions, db, cfg, llmCfg) *Runtime
func (r *Runtime) Run(ctx, ch, sess, msg) (response, error)
```

**Dependencies:** `provider/`, `tool/`, `session/`, `store/`

---

#### `internal/agent/subagent/` — Sub-Agent Management

**Files:** `subagent.go` (SubAgentManager), `agent_manager.go`, `agent_tool.go`, `team.go` (TeamCoordinator)

**Exports:**
```
type SubAgentManager struct { ... }
type AgentManager struct { ... }
type TeamCoordinator struct { ... }

func (m *SubAgentManager) Spawn(ctx, spec, task) (*Result, error)
func (m *SubAgentManager) SpawnParallel(ctx, specs, tasks, strategy) ([]*Result, error)
```

**Dependencies:** `runtime/`, `execution/`, `tool/`, `session/`, `store/`

---

#### `internal/agent/` (orchestration layer) — Cognitive Loop

**Files:** `cognitive.go` (~250 lines), `cognitive_types.go`, `cognitive_prompts.go`

**What stays:**
- `CognitiveAgent` struct with concrete type fields from all sub-packages
- `NewCognitiveAgent()` — constructs and wires all sub-package instances
- `Run()` — the 5-phase loop (PERCEIVE → PLAN → ACT → OBSERVE → REFLECT)
- `CognitiveAgentOptions` struct (simplified — most fields become direct sub-package configs)

**What moves out:** Everything else.

**Imports:** All 11 sub-packages + Layer 0 packages.

```
// agent/cognitive.go — the orchestrator
type CognitiveAgent struct {
    planner      *planning.Planner
    treePlanner  *planning.TreePlanner
    executor     *execution.Executor
    healer       *healing.AutoHealer
    perceiver    *perception.Perceiver
    observer     *observation.Observer
    reflector    *reflection.Reflector
    recorder     *recording.SessionRecorder
    runtime      *runtime.Runtime
    checkpoint   *checkpoint.CheckpointStore
    provider     provider.Provider
    // ... config, DB, sessions, etc.
}
```

## Interface Strategy

Only 4 interfaces are defined. Everything else uses concrete types.

| Interface | Package | Reason |
|-----------|---------|--------|
| `Provider` | `provider/` | 3 implementations (Claude, OpenAI, Retry) |
| `ToolExecutor` | `execution/` | healing/ needs to replay fixed tool calls without importing execution/ |
| `DashboardEmitter` | `agent/` (existing) | Avoids agent → dashboard circular dependency |
| `ContextManager` | `runtime/` | PipelineContextManager is the impl; interface enables testing |

**Principle:** Go interfaces belong at the call site, not the implementation site. Since each sub-package has exactly one consumer (agent/), and one implementation, there's no need for an interface. The sub-package's public API IS the contract.

## Migration Plan

### Phase 1: Leaf Packages (lowest risk)

**Packages:** `provider/`, `observation/`, `recording/`, `healing/`, `checkpoint/`

**Steps per package:**
1. `mkdir internal/agent/<pkg>`
2. `git mv` source files into new directory
3. Change `package agent` → `package <pkg>` in moved files
4. Fix imports in moved files (add `import "github.com/.../internal/agent"` where they reference types still in agent/)
5. Fix imports in remaining agent/ files (add `import ".../internal/agent/<pkg>"`)
6. Fix imports in gateway/, cmd/, eval/ consumers
7. `go build ./... && go test ./...`

**Estimated:** 2-3 files per package, ~50 import fixes each. ~3 hours total.

### Phase 2: Domain Packages

**Packages:** `planning/`, `perception/`, `reflection/`

**Additional complexity:** These packages import from Phase 1 packages (e.g., planning/ imports provider/). Phase 1 must be complete and stable first.

**Estimated:** 3-5 files per package. ~4 hours total.

### Phase 3: Core Packages (highest risk)

**Packages:** `execution/`, `runtime/`

**Additional complexity:** These are the largest packages and the most heavily referenced by gateway/. `runtime/` is the entry point for simple mode. `execution/` is the entry point for the cognitive ACT phase.

**Execution plan:**
- `execution/` moves `act.go` + `concurrent.go` + `tool_cache.go`
- `runtime/` moves `runtime.go` + `stream.go` + `compression.go` + `context_manager.go` + `speculative.go` + `sidechain.go`
- gateway/ needs the most import updates here

**Estimated:** 6-8 files, ~80 import fixes across codebase. ~5 hours.

### Phase 4: Sub-Agent Package

**Package:** `subagent/`

**Files:** `subagent.go`, `agent_manager.go`, `agent_tool.go`, team-related files

**Dependencies on:** `runtime/`, `execution/` (both already moved in Phase 3)

**Estimated:** ~4 hours.

### Phase 5: Contract agent/ Orchestrator

**What's left in agent/:**
- `cognitive.go` — rewrite to use sub-package constructors
- `cognitive_types.go` — type definitions used across cognitive loop
- `cognitive_prompts.go` — prompt templates (consider moving to a separate `prompts/` package later)

**Cleanup:**
- Remove any remaining `import "github.com/.../internal/rl"` or `import "github.com/.../internal/evolution"` references
- Remove old file stubs (`.bak` files)
- Remove unused functions exposed during the migration

**Estimated:** ~2 hours.

### Post-Migration Verification

Per phase and after final phase:
```bash
CGO_ENABLED=1 go build -tags "fts5" ./...
CGO_ENABLED=1 go test -tags "fts5" ./... -count=1
golangci-lint run ./...
```

## Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Import cycle introduced between sub-packages | Medium | High — build failure | `go build` after each file move. CI gate. |
| Test breakage from changed package paths | High | Medium — test failures | Run full test suite after each phase. |
| gateway/ wiring breaks | High | High — app won't start | Phase 3 includes explicit gateway import fix pass. |
| Accidental public API changes | Low | Medium — external consumers break | Only move files, don't rename exported symbols. |
| Type moved to wrong package | Medium | Medium — needs re-reorganization | Review each phase's file assignments before starting. |

## Out of Scope

- **Optimization loop fix.** Recording data → strategy improvement is a separate design effort. This refactoring only creates the `recording/` package; it doesn't build the feedback loop.
- **Auto-heal LLM enhancement.** Adding LLM-based fix strategies to healing/ is out of scope. This only moves existing heuristic code.
- **MCTS reward calibration.** Tuning `simulateHeuristic` weights is out of scope.
- **Gateway Builder pattern.** Gateway wiring stays as-is (only import paths change). A `CognitiveAgentBuilder` or similar factory is a future enhancement.
- **prompts/ extraction.** `cognitive_prompts.go` stays in agent/ for now. A separate prompts/ package adds complexity without clear benefit at this stage.
- **config/ refactoring.** `config.AgentConfig` and friends are untouched.
- **File content changes.** This is a structural move. No logic changes, no bug fixes, no feature additions. Pure reorganization.
