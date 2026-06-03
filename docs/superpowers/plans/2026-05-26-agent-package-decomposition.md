# Agent Package Decomposition — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decompose `internal/agent/` (90+ files, ~23K lines) into 1 orchestration layer + 11 domain sub-packages, following a 5-Phase migration.

**Spec:** `docs/superpowers/specs/2026-05-26-agent-package-decomposition-design.md`

**Architecture:** Each sub-package owns its domain types and logic. Dependencies form a strict DAG (no cycles). The top-level `agent/` package becomes a thin orchestrator (~300 lines). External consumers (gateway/, cmd/, eval/) update import paths mechanically.

**Tech Stack:** Go 1.25.9, CGO_ENABLED=1, -tags fts5

**Working directory:** `ssh dev-server-senyu` → `~/dev/IronClaw`

**Path prefix for all new packages:** `~/dev/IronClaw/internal/agent/`

---

## Dependency DAG

```
                    ┌──────────────┐
                    │   provider/  │
                    └──────┬───────┘
           ┌───────────────┼───────────────┐
           │               │               │
    ┌──────▼──────┐ ┌──────▼──────┐ ┌──────▼──────┐
    │  planning/  │ │ perception/ │ │ reflection/ │
    └──────┬──────┘ └─────────────┘ └─────────────┘
           │
    ┌──────▼──────┐ ┌──────────────┐
    │ execution/  │ │ observation/ │
    └──────┬──────┘ └──────┬───────┘
           │               │
           └───────┬───────┘
                   │
    ┌──────────────┼──────────────┐
    │              │              │
┌───▼──────┐ ┌─────▼─────┐ ┌─────▼──────┐
│ runtime/ │ │subagent/  │ │ recording/ │
└──────────┘ └───────────┘ └────────────┘

Leaves (zero agent-internal deps): provider/, observation/, recording/, healing/, checkpoint/
```

---

## File Assignment Map

### provider/ — LLM abstraction (7 files)
| From `internal/agent/` | Role |
|------------------------|------|
| `provider.go` | Provider interface, CompletionRequest/Response, ToolDefinition, CompletionMessage, ToolUseBlock |
| `openai.go` | OpenAIProvider |
| `retry.go` | RetryProvider |
| `model_context.go` | ModelContextWindow() |
| `tokenizer.go` | Tokenizer interface + TiktokenTokenizer |
| `prompt_cache.go` | Prompt cache helpers |
| `cache_metrics.go` | CacheMetrics |

Tests: `openai_test.go`, `retry_test.go`, `tokenizer_test.go`, `prompt_cache_test.go`, `cache_metrics_test.go`

### observation/ — assertions & observation (2 files)
| From `internal/agent/` | Role |
|------------------------|------|
| `assertion.go` | GenerateAssertions(), AssertionResult, error patterns |
| `observe.go` | Observer, Observation types |

Tests: `assertion_test.go`

### recording/ — session recording & replay (2 files)
| From `internal/agent/` | Role |
|------------------------|------|
| `recorder.go` | SessionRecorder, RecordingEvent |
| `replayer.go` | SessionReplayer, SessionDiff |

Tests: `recorder_test.go`, `replayer_test.go`

### healing/ — auto-heal (1 file)
| From `internal/agent/` | Role |
|------------------------|------|
| `auto_heal.go` | AutoHealer, error patterns, fix strategies |

Tests: `auto_heal_test.go`

### checkpoint/ — task persistence (1 file)
| From `internal/agent/` | Role |
|------------------------|------|
| `checkpoint.go` | SQLiteCheckpointStore |

Tests: `checkpoint_test.go`

### planning/ — plan generation (2 files)
| From `internal/agent/` | Role |
|------------------------|------|
| `plan.go` | Planner, TaskPlan, SubTask types |
| `planner_tree.go` | TreePlanner (MCTS search) |

Tests: `planner_tree_test.go`

### perception/ — context gathering (5 files)
| From `internal/agent/` | Role |
|------------------------|------|
| `perceive.go` | Perceiver, CognitiveState, Goal |
| `project_scanner.go` | ProjectContextScanner |
| `git_context.go` | GitContextProvider |
| `context_budget.go` | ContextBudgetAllocator |
| `failure_context.go` | Failure context enrichment |

Tests: `project_scanner_test.go`, `git_context_test.go`, `context_budget_test.go`, `failure_context_test.go`

### reflection/ — post-action reflection (1 file)
| From `internal/agent/` | Role |
|------------------------|------|
| `reflect.go` | Reflector |

Tests: `reflect_test.go`

### execution/ — tool dispatch (6 files)
| From `internal/agent/` | Role |
|------------------------|------|
| `act.go` | Executor |
| `concurrent.go` | Parallel tool execution |
| `tool_cache.go` | ToolResultCache |
| `permission.go` | Permission handling |
| `circuit_breaker.go` | CircuitBreaker |
| `aggregator.go` | Aggregator (multi-agent result synthesis) |

Tests: `concurrent_test.go`, `tool_cache_test.go`, `permission_test.go`

### runtime/ — simple mode + streaming (10 files)
| From `internal/agent/` | Role |
|------------------------|------|
| `runtime.go` | Runtime (simple agent mode) |
| `stream.go` | Streaming response handling |
| `compression.go` | Compression pipeline |
| `compaction.go` | History compaction |
| `context_manager.go` | ContextManager interface + PipelineContextManager |
| `speculative.go` | Speculative execution |
| `context.go` | safeTrimHistory() |
| `sidechain.go` | SidechainRecorder, SidechainEntry |
| `token_budget.go` | BudgetCheck, BudgetAction |
| `background.go` | BackgroundAgent tracking |

Tests: `compression_test.go`, `context_manager_test.go`, `speculative_test.go`, `sidechain_test.go`, `token_budget_test.go`, `background_test.go`

### subagent/ — sub-agents & teams (21 files)
| From `internal/agent/` | Role |
|------------------------|------|
| `subagent.go` | SubAgentManager |
| `subagent_context.go` | SubagentContext, MaxForkDepth |
| `subagent_result.go` | Result extraction |
| `agent_manager.go` | AgentManager |
| `agent_tool.go` | AgentTool |
| `agent_hooks.go` | Agent lifecycle hooks |
| `agent_mcp.go` | AgentMCPManager |
| `spec.go` | AgentSpec |
| `team.go` | TeamCoordinator |
| `team_manager.go` | TeamManager |
| `team_task.go` | Team task types |
| `team_message.go` | Team messaging |
| `orchestrator.go` | AgentOrchestrator, AgentTask/AgentResult |
| `debate.go` | DebateConfig, BuildDebatePlan |
| `fork.go` | BuildForkMessages, CheckForkDepth |
| `task_context.go` | TaskContext |
| `trace.go` | Trace, TraceCollector |
| `backend.go` | BackendType, BackendConfig |
| `backend_docker.go` | Docker backend |
| `backend_ipc.go` | IPC backend |
| `backend_subprocess.go` | Subprocess backend |

Tests: `subagent_test.go`, `subagent_result_test.go`, `agent_manager_test.go`, `spec_test.go`, `team_test.go`, `team_manager_test.go`, `team_message_test.go`, `team_task_test.go`, `orchestrator_test.go`, `backend_test.go`, `subagent_integration_test.go`

### agent/ — orchestration layer (stays, ~4 files + integration tests)
| File | Role |
|------|------|
| `cognitive.go` | CognitiveAgent struct + Run() loop (rewritten with sub-package imports) |
| `cognitive_types.go` | Remaining shared types not owned by any sub-package |
| `cognitive_prompts.go` | Prompt templates |
| `dashboard_emitter.go` | DashboardEmitter interface |
| `cognitive_integration_test.go` | Integration test |
| `integration_test.go` | Integration test |
| `hook_integration_test.go` | Integration test |

---

## Consumer Impact Map

Files OUTSIDE `internal/agent/` that import `"github.com/Forest-Isle/IronClaw/internal/agent"` and which new imports they need:

| Consumer | New sub-package imports needed |
|----------|-------------------------------|
| `internal/gateway/gateway.go` | +provider, +runtime, +subagent, +execution, +planning, +perception, +reflection, +recording, +healing, +checkpoint, +observation |
| `internal/gateway/init_agent.go` | +provider, +runtime |
| `internal/gateway/init_cognitive.go` | +planning, +execution, +perception, +reflection, +recording, +healing, +checkpoint, +observation |
| `internal/gateway/init_multiagent.go` | +subagent |
| `internal/gateway/headless.go` | +runtime |
| `internal/channel/tui/emitter.go` | (stays — DashboardEmitter remains in agent/) |
| `internal/eval/classifier.go` | +observation |
| `internal/eval/cognitive_runner.go` | +planning, +execution |
| `internal/eval/judge.go` | +observation |
| `internal/eval/adaptive.go` | +execution |
| `cmd/ironclaw/agent_run.go` | +runtime |
| `cmd/ironclaw/eval.go` | +planning, +execution, +observation |

---

## Common Phase Procedure

For each package extraction, follow this pattern. Within each Phase, packages are independent and can be done in any order.

1. `mkdir -p internal/agent/<pkg>`
2. `git mv` source + test files from `internal/agent/` to `internal/agent/<pkg>/`
3. Replace `package agent` with `package <pkg>` in all moved `.go` files
4. Add `import agent "github.com/Forest-Isle/IronClaw/internal/agent"` to moved files that reference types still in `agent/`
5. Add `import "github.com/Forest-Isle/IronClaw/internal/agent/<pkg>"` to remaining `internal/agent/*.go` files that use the moved types
6. Qualify moved types in remaining agent/ files (e.g., `Provider` → `provider.Provider`)
7. Update imports in consumer files (gateway/, cmd/, eval/)
8. `CGO_ENABLED=1 go build -tags fts5 ./...`
9. Fix any `undefined` errors iteratively
10. `CGO_ENABLED=1 go test -tags fts5 ./internal/agent/<pkg>/... -count=1`
11. `git add -A && git commit -m "refactor(agent): extract <pkg>/ sub-package"`

---

## Phase 1: Leaf Packages (zero cross-deps, lowest risk)

These 5 packages import ZERO other agent sub-packages. They can be extracted in any order and committed independently.

### Task 1.1: Extract provider/

**Move:** `provider.go`, `openai.go`, `retry.go`, `model_context.go`, `tokenizer.go`, `prompt_cache.go`, `cache_metrics.go`
**Tests:** `openai_test.go`, `retry_test.go`, `tokenizer_test.go`, `prompt_cache_test.go`, `cache_metrics_test.go`

- [ ] Create dir, move files, fix package name
- [ ] In moved files: `agent.` references (check each file with `grep -n 'agent\.'`)
- [ ] In remaining `internal/agent/*.go`: add `provider` import, qualify `Provider`→`provider.Provider`, `CompletionRequest`→`provider.CompletionRequest`, `CompletionResponse`→`provider.CompletionResponse`, `CompletionMessage`→`provider.CompletionMessage`, `ToolDefinition`→`provider.ToolDefinition`, `ToolUseBlock`→`provider.ToolUseBlock`, `ToolUseResult`→`provider.ToolUseResult`, `NewRetryProvider`→`provider.NewRetryProvider`, `NewTiktokenTokenizer`→`provider.NewTiktokenTokenizer`, `ModelContextWindow`→`provider.ModelContextWindow`, `CacheMetrics`→`provider.CacheMetrics`
- [ ] In gateway/: `agent.NewClaudeProvider`→`provider.NewClaudeProvider`, `agent.NewOpenAIProvider`→`provider.NewOpenAIProvider`, `agent.NewRetryProvider`→`provider.NewRetryProvider`, `agent.Provider`→`provider.Provider`
- [ ] In cmd/ironclaw/agent_run.go: update Provider references
- [ ] Build → test → commit

### Task 1.2: Extract observation/

**Move:** `assertion.go`, `observe.go`
**Tests:** `assertion_test.go`

- [ ] Create dir, move files, fix package name
- [ ] In remaining agent/*.go: qualify `Observation`→`observation.Observation`, `ObservationMetadata`→`observation.ObservationMetadata`, `AssertionResult`→`observation.AssertionResult`, `Observer`→`observation.Observer`, `generateAssertions`→`observation.GenerateAssertions`
- [ ] Files needing updates: `act.go`, `concurrent.go`, `auto_heal.go`, `cognitive.go`, `reflect.go`
- [ ] Build → test → commit

### Task 1.3: Extract recording/

**Move:** `recorder.go`, `replayer.go`
**Tests:** `recorder_test.go`, `replayer_test.go`

- [ ] Create dir, move files, fix package name
- [ ] In agent/*.go: qualify `SessionRecorder`→`recording.SessionRecorder`, `SessionReplayer`→`recording.SessionReplayer`, `RecordingEvent`→`recording.RecordingEvent`, `SessionDiff`→`recording.SessionDiff`, `ReplayHandler`→`recording.ReplayHandler`
- [ ] In gateway/init_cognitive.go: update recorder setup
- [ ] Build → test → commit

### Task 1.4: Extract healing/

**Move:** `auto_heal.go`
**Tests:** `auto_heal_test.go`

- [ ] Create dir, move files, fix package name
- [ ] Note: `auto_heal.go` imports `observation/` for `Observation`, `AssertionResult` types. Add the import.
- [ ] In agent/*.go and execution/: qualify `AutoHealer`→`healing.AutoHealer`, `AutoHealResult`→`healing.AutoHealResult`, `AutoHealFix`→`healing.AutoHealFix`, `AutoHealContext`→`healing.AutoHealContext`
- [ ] Build → test → commit

### Task 1.5: Extract checkpoint/

**Move:** `checkpoint.go`
**Tests:** `checkpoint_test.go`

- [ ] Create dir, move files, fix package name
- [ ] In agent/cognitive.go: qualify `CheckpointStore`→`checkpoint.CheckpointStore`, `NewSQLiteCheckpointStore`→`checkpoint.NewSQLiteCheckpointStore`
- [ ] In gateway/init_cognitive.go: update checkpoint setup
- [ ] Build → test → commit

---

## Phase 2: Domain Packages

These depend on Phase 1 packages + Layer 0 externals. Must be done AFTER Phase 1 is stable.

### Task 2.1: Extract planning/

**Move:** `plan.go`, `planner_tree.go`
**Tests:** `planner_tree_test.go`

**New imports needed:** `provider/`

- [ ] Create dir, move files, fix package name
- [ ] In moved files: `CompletionRequest`→`provider.CompletionRequest`, `Provider`→`provider.Provider`
- [ ] In agent/*.go and execution/: qualify `TaskPlan`→`planning.TaskPlan`, `SubTask`→`planning.SubTask`, `Planner`→`planning.Planner`, `TreePlanner`→`planning.TreePlanner`, `PlanTreeNode`→`planning.PlanTreeNode`
- [ ] In gateway/init_cognitive.go: update planner setup
- [ ] Build → test → commit

### Task 2.2: Extract perception/

**Move:** `perceive.go`, `project_scanner.go`, `git_context.go`, `context_budget.go`, `failure_context.go`
**Tests:** `project_scanner_test.go`, `git_context_test.go`, `context_budget_test.go`, `failure_context_test.go`

- [ ] Create dir, move files, fix package name
- [ ] In agent/cognitive.go: qualify `CognitiveState`→`perception.CognitiveState`, `Perceiver`→`perception.Perceiver`, `Goal`→`perception.Goal`, `ProjectContext`→`perception.ProjectContext`, `GitState`→`perception.GitState`, etc.
- [ ] In gateway/init_cognitive.go: update perceiver setup
- [ ] Build → test → commit

### Task 2.3: Extract reflection/

**Move:** `reflect.go`
**Tests:** `reflect_test.go`

**New imports needed:** `provider/`

- [ ] Create dir, move files, fix package name
- [ ] In agent/cognitive.go: qualify `Reflector`→`reflection.Reflector`, `ReflectionResult`→`reflection.ReflectionResult`
- [ ] Build → test → commit

---

## Phase 3: Core Packages

These are the largest and most-referenced packages. HIGHEST RISK.

### Task 3.1: Extract execution/

**Move:** `act.go`, `concurrent.go`, `tool_cache.go`, `permission.go`, `circuit_breaker.go`, `aggregator.go`
**Tests:** `concurrent_test.go`, `tool_cache_test.go`, `permission_test.go`

**New imports needed:** `provider/`, `planning/`, `observation/`, `healing/`

- [ ] Create dir, move files, fix package name
- [ ] Fix imports in moved files — `TaskPlan`→`planning.TaskPlan`, `SubTask`→`planning.SubTask`, `Observation`→`observation.Observation`, `AssertionResult`→`observation.AssertionResult`, `AutoHealer`→`healing.AutoHealer`
- [ ] In agent/cognitive.go: qualify `Executor`→`execution.Executor`, `ToolExecutor`→`execution.ToolExecutor`
- [ ] In gateway/init_cognitive.go and gateway/gateway.go: update executor setup and type references
- [ ] In eval/: update executor and observation references
- [ ] Build → test → commit (THIS IS THE RISKIEST COMMIT)

### Task 3.2: Extract runtime/

**Move:** `runtime.go`, `stream.go`, `compression.go`, `compaction.go`, `context_manager.go`, `speculative.go`, `context.go`, `sidechain.go`, `token_budget.go`, `background.go`
**Tests:** `compression_test.go`, `context_manager_test.go`, `speculative_test.go`, `sidechain_test.go`, `token_budget_test.go`, `background_test.go`

**New imports needed:** `provider/`

- [ ] Create dir, move files, fix package name
- [ ] In agent/*.go and gateway/: qualify `Runtime`→`runtime.Runtime`, `ContextManager`→`runtime.ContextManager`, `PipelineContextManager`→`runtime.PipelineContextManager`, etc.
- [ ] In gateway/init_agent.go and cmd/ironclaw/agent_run.go: update runtime references
- [ ] Build → test → commit

---

## Phase 4: Sub-Agent Package

### Task 4.1: Extract subagent/

**Move:** All 21 files + all test files (see File Assignment Map above)

**New imports needed:** `provider/`, `runtime/`, `execution/`, `planning/`, `observation/`

- [ ] Create dir, move files, fix package name
- [ ] Fix imports in moved files for provider, runtime, execution, planning, observation types
- [ ] In agent/cognitive.go: qualify `SubAgentManager`→`subagent.SubAgentManager`, `AgentManager`→`subagent.AgentManager`, `TeamCoordinator`→`subagent.TeamCoordinator`, etc.
- [ ] In gateway/init_multiagent.go: update subagent references
- [ ] In gateway/gateway.go: update subagent type references
- [ ] Build → test → commit

---

## Phase 5: Shrink agent/ to Orchestrator

### Task 5.1: Clean and finalize

- [ ] Delete stale files: `rm -f internal/agent/*.bak internal/agent/*.current`
- [ ] Verify `cognitive.go` compiles with all 11 sub-package imports
- [ ] Verify `cognitive_types.go` only contains types NOT owned by any sub-package
- [ ] Full build: `CGO_ENABLED=1 go build -tags fts5 ./...`
- [ ] Full test: `CGO_ENABLED=1 go test -tags fts5 ./... -count=1 -timeout 300s`
- [ ] Lint: `golangci-lint run ./...`
- [ ] Import cycle check: `go vet ./internal/agent/...`
- [ ] Final commit

---

## Post-Migration Verification

```bash
# On dev-server-senyu, in ~/dev/IronClaw:
export PATH=$HOME/go/bin:$PATH

# Full build
CGO_ENABLED=1 go build -tags fts5 ./...

# Full test suite
CGO_ENABLED=1 go test -tags fts5 ./... -count=1 -timeout 300s

# Lint
golangci-lint run ./...

# Verify no import cycles in agent tree
go vet ./internal/agent/...

# Verify file count per package
find internal/agent -name '*.go' ! -name '*_test.go' | sed 's|/[^/]*$||' | sort | uniq -c | sort -rn
```

Expected output: each sub-package has < 15 source files, agent/ has < 8.

---

## Risk Mitigation

- **Each Phase commits independently.** If Phase 3 breaks, Phases 1-2 are already committed; `git revert` the bad commit.
- **Build after every Task.** Never batch multiple package extractions into one build.
- **Manual review of type qualifiers.** `grep -n` before committing to catch missed renames.
- **Tests follow source files.** Each `_test.go` stays with its source. Integration tests stay in `agent/`.
- **Consumer updates last.** Fix agent/ internals first, then update gateway/cmd/eval consumers.
