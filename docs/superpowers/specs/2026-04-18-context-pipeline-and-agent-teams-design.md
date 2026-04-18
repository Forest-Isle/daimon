# Context Pipeline Upgrade + Agent Teams Design

**Date**: 2026-04-18
**Scope**: Phase 1 (Context Compression + Speculative Execution) + Phase 2 (Task Ledger + Agent Teams)
**Approach**: Modular components — independent modules with clean interfaces, wired via gateway
**Target**: Both simple Runtime and CognitiveAgent (shared infrastructure)

---

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Target modes | Both (shared infra) | Prevents gap from widening; common `ContextManager` interface |
| Speculative execution | Moderate | Execute read-only tools when complete `tool_use` block parsed during streaming; safe, proven by Claude Code |
| Agent Teams coordination | Hybrid | Shared task list + lightweight notification channel |
| Overall architecture | Modular components | Go-idiomatic, testable in isolation, matches existing gateway wiring |

---

## Phase 1A: Unified Context Manager

### Problem

Simple `Runtime` uses the 5-layer `CompressionPipeline`, while `CognitiveAgent` uses legacy `CompactHistory` (single LLM summary when >40 messages). Neither has reactive compression (API returns 413 → retry) or system prompt cache boundaries.

### Interface

```go
// internal/agent/context_manager.go

type ContextManager interface {
    // Compress runs the compression pipeline, returning true if any layer acted.
    Compress(ctx context.Context, sess *session.Session, systemPrompt string) (bool, error)

    // ReactiveCompress runs emergency compression after an API error (e.g. 413).
    // More aggressive than Compress — skips threshold checks.
    ReactiveCompress(ctx context.Context, sess *session.Session, systemPrompt string) error

    // Utilization returns current estimated context utilization (0.0 - 1.0).
    Utilization(sess *session.Session, systemPrompt string) float64

    // SplitSystemPrompt separates static (cacheable) and dynamic (per-turn) sections.
    SplitSystemPrompt(full string) (static, dynamic string)
}
```

### Implementation: `PipelineContextManager`

Wraps the existing `CompressionPipeline` + `TokenBudget` + new capabilities.

**Enhanced compression pipeline** (6 layers):

1. `ToolOutputPrePruneLayer` — truncates old large `tool_result` bodies (existing)
2. `ToolEvictionLayer` — persists large results via `ResultStore` (existing)
3. `TurnSummarizationLayer` — LLM summary of older history half (existing)
4. `OldContextRemovalLayer` — drops oldest third of messages (existing)
5. `EmergencyTruncationLayer` — keeps last N turns (existing)
6. **`ReactiveCompactLayer`** (NEW) — triggered only by `ReactiveCompress`, bypasses thresholds, aggressive single-pass summary preserving tool pairing

**Reactive compression flow** (with circuit breaker):

```
API returns 413 or context_length_exceeded
  → if hasAttemptedReactiveCompact: return error (no infinite loops)
  → hasAttemptedReactiveCompact = true
  → ReactiveCompress(sess, systemPrompt)
    → Skip threshold checks, run layers 4+5+6 directly
    → If still over budget: emergency keep last 10 turns only
  → Retry API call with compressed context
  → If still fails → return error to user
  → On success: reset hasAttemptedReactiveCompact for next iteration
```

The `hasAttemptedReactiveCompact` flag is per-iteration (reset on each new streaming loop), preventing the compress-retry cycle from looping more than once.

**System prompt splitting**:

`SplitSystemPrompt` scans for `<!-- DYNAMIC_CONTEXT -->` marker. Everything above is static (tool definitions, personality, skill descriptions), everything below is dynamic (memory results, git state, project context). The static part gets `CacheControl: ephemeral` on the API call.

### Integration

- `Runtime.HandleMessage`: replace `compressionPipeline.Run()` → `contextManager.Compress()`. Add `ReactiveCompress` in 413 error handler.
- `CognitiveAgent.HandleMessage`: replace `CompactHistory()` → `contextManager.Compress()`. Both modes get the full 5-layer pipeline + reactive compression.
- `buildSystemPrompt` / `cognitive_prompts.go`: insert `<!-- DYNAMIC_CONTEXT -->` between static and dynamic sections.

### Files Changed

| File | Change |
|------|--------|
| `internal/agent/context_manager.go` | NEW — interface + `PipelineContextManager` |
| `internal/agent/compression.go` | Add `ReactiveCompactLayer`; export `RunForced()` which skips per-layer threshold checks and runs layers unconditionally |
| `internal/agent/runtime.go` | Replace `compressionPipeline.Run()` → `contextManager.Compress()`, add 413 retry |
| `internal/agent/cognitive.go` | Replace `CompactHistory()` → `contextManager.Compress()` |
| `internal/agent/stream.go` | Use `SplitSystemPrompt` for `CacheControl` placement |
| `internal/agent/cognitive_prompts.go` | Add `<!-- DYNAMIC_CONTEXT -->` marker |
| `internal/gateway/init_multiagent.go` | Wire `PipelineContextManager` to both runtime and cognitive |

---

## Phase 1B: Speculative Tool Executor

### Problem

Tool execution begins only after the model finishes streaming. For turns with read-only tool calls, this wastes 200-500ms of latency.

### Design

```go
// internal/agent/speculative.go

type SpeculativeExecutor struct {
    registry    *tool.Registry
    maxInFlight int                         // default 3
    results     map[string]*speculativeResult
    mu          sync.Mutex
}

type speculativeResult struct {
    toolUseID string
    toolName  string
    result    *tool.Result
    err       error
    done      chan struct{}
    cancelled bool
}

// TryLaunch is called when a complete tool_use block is parsed from the stream.
// Returns true if the tool was launched speculatively.
func (se *SpeculativeExecutor) TryLaunch(ctx context.Context, toolUseID, toolName, input string) bool

// Collect returns the pre-computed result for a tool_use ID, or nil.
func (se *SpeculativeExecutor) Collect(toolUseID string) (*tool.Result, error)

// CancelAll cancels all in-flight speculative executions.
func (se *SpeculativeExecutor) CancelAll()

// Reset clears state for a new iteration.
func (se *SpeculativeExecutor) Reset()
```

### Launch criteria (all must pass)

1. `tool.IsReadOnly(t)` returns true
2. In-flight count < `maxInFlight`
3. Tool passes permission check without user approval
4. No blocking pre-execution hooks

### Stream integration

The Anthropic streaming API sends `content_block_stop` for each tool_use block as it completes, before the full message ends. This is the trigger:

```go
// stream.go enhancement
func (it *claudeStreamIterator) onContentBlockStop(block ContentBlock) {
    if block.Type == "tool_use" {
        it.pendingToolBlocks = append(it.pendingToolBlocks, ToolUseBlock{
            ID: block.ID, Name: block.Name, Input: block.Input,
        })
    }
}
```

**Modified streaming loop** (`Runtime.HandleMessage`):

```
stream.Next() → accumulate text
  → on each complete tool_use block during stream:
      speculativeExecutor.TryLaunch(ctx, id, name, input)
  → on Done: extract toolCalls
  → for each toolCall:
      if result := speculativeExecutor.Collect(id); result != nil:
          use cached result (skip execution)
      else:
          execute normally
```

### Scope

- **Simple Runtime**: full speculative execution during streaming.
- **CognitiveAgent**: not applicable — ACT phase uses `Executor.RunWithContext` (no streaming). Gains speculative execution when/if PLAN adopts streaming.

### Metrics

Track `speculative_hit` and `speculative_miss` counts. Feed into evolution trajectory data via `ToolExecEvent.Metadata`.

### Files Changed

| File | Change |
|------|--------|
| `internal/agent/speculative.go` | NEW — `SpeculativeExecutor` |
| `internal/agent/stream.go` | Emit `pendingToolBlocks` on `content_block_stop` |
| `internal/agent/runtime.go` | Wire speculative executor into streaming loop |
| `internal/agent/concurrent.go` | `executeToolCall` accepts optional pre-computed result |
| `internal/config/config.go` | Add `SpeculativeExecution` config |
| `internal/gateway/init_multiagent.go` | Wire speculative executor to runtime |

---

## Phase 2A: Unified Task Ledger

### Problem

Multiple execution paths (Runtime, CognitiveAgent, sub-agents, scheduler) run independently with no shared visibility. No way to answer "what is the system doing?" without digging into multiple subsystems.

### Interface

```go
// internal/taskledger/ledger.go

type TaskState string
const (
    TaskPending   TaskState = "pending"
    TaskClaimed   TaskState = "claimed"
    TaskRunning   TaskState = "running"
    TaskCompleted TaskState = "completed"
    TaskFailed    TaskState = "failed"
    TaskCancelled TaskState = "cancelled"
    TaskBlocked   TaskState = "blocked"
)

type TaskKind string
const (
    KindUserRequest  TaskKind = "user_request"
    KindCognitiveAct TaskKind = "cognitive_act"
    KindSubAgent     TaskKind = "sub_agent"
    KindScheduled    TaskKind = "scheduled"
    KindTeamTask     TaskKind = "team_task"
)

type Task struct {
    ID            string
    ParentID      string
    SessionID     string
    Kind          TaskKind
    State         TaskState
    AgentID       string
    Description   string
    CreatedAt     time.Time
    UpdatedAt     time.Time
    HeartbeatAt   time.Time
    Result        string
    Error         string
    Metadata      map[string]string  // includes "depends_on" for deps
}

type TaskLedger interface {
    Register(ctx context.Context, task Task) error
    UpdateState(ctx context.Context, id string, state TaskState, result string) error
    Heartbeat(ctx context.Context, id string) error

    Get(ctx context.Context, id string) (*Task, error)
    ListByState(ctx context.Context, states ...TaskState) ([]Task, error)
    ListByParent(ctx context.Context, parentID string) ([]Task, error)
    GetTree(ctx context.Context, rootID string) ([]Task, error)

    ClaimNext(ctx context.Context, agentID string, kinds ...TaskKind) (*Task, error)
    AddTasks(ctx context.Context, tasks []Task) error

    DetectStale(ctx context.Context, timeout time.Duration) ([]Task, error)
    Cancel(ctx context.Context, id string) error
}
```

### Storage

Migration 019 (`task_ledger` table):

```sql
CREATE TABLE IF NOT EXISTS task_ledger (
    id TEXT PRIMARY KEY,
    parent_id TEXT,
    session_id TEXT,
    kind TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending',
    agent_id TEXT,
    description TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    heartbeat_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    result TEXT,
    error TEXT,
    metadata TEXT
);
CREATE INDEX idx_task_ledger_state ON task_ledger(state);
CREATE INDEX idx_task_ledger_parent ON task_ledger(parent_id);
CREATE INDEX idx_task_ledger_session ON task_ledger(session_id);
CREATE INDEX idx_task_ledger_heartbeat ON task_ledger(state, heartbeat_at);
```

`ClaimNext` uses SQLite single-writer guarantee for atomic claiming:

```sql
UPDATE task_ledger
SET state = 'claimed', agent_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = (
    SELECT id FROM task_ledger
    WHERE state = 'pending' AND kind IN (?)
    ORDER BY created_at ASC LIMIT 1
) RETURNING *;
```

### Integration points

| Caller | When | Kind |
|--------|------|------|
| `Runtime.HandleMessage` | User message | `KindUserRequest` |
| `CognitiveAgent` ACT | Subtask start | `KindCognitiveAct` |
| `AgentTool.Execute` | Spawn/fork/bg | `KindSubAgent` |
| `Scheduler` | Cron trigger | `KindScheduled` |
| Agent Teams planner | Task creation | `KindTeamTask` |

### Stale detection

Background goroutine every 60s: tasks with `state IN (running, claimed)` and `heartbeat_at < now() - stale_timeout` are marked failed. Sub-agent tasks attempt cancel via `BackgroundManager`.

### Relationship to `checkpoint.go`

Complementary. Ledger tracks *what* is running; checkpoints track *where* a cognitive task left off. `/resume` re-registers the task as `running` in the ledger.

### Slash commands

- `/tasks` — list active tasks
- `/tasks <id>` — show task + child hierarchy
- `/tasks cancel <id>` — cancel task and children

### Files Changed

| File | Change |
|------|--------|
| `internal/taskledger/` | NEW — `ledger.go`, `store.go`, `stale.go` |
| `internal/store/migrations/019_task_ledger.sql` | NEW |
| `internal/agent/runtime.go` | Register/update tasks |
| `internal/agent/cognitive.go` | Register cognitive subtasks |
| `internal/agent/agent_tool.go` | Register sub-agent tasks |
| `internal/scheduler/scheduler.go` | Register scheduled tasks |
| `internal/channel/tui/commands.go` | `/tasks` commands |
| `internal/gateway/gateway.go` | Wire ledger, start stale detector |

---

## Phase 2B: Agent Teams

### Problem

IronClaw's multi-agent is strictly hierarchical. No peer-to-peer collaboration — multiple agents can't work through a shared task list in parallel.

### Design

```go
// internal/taskledger/team.go

type TeamConfig struct {
    WorkerCount    int
    WorkerSpec     string
    PlannerSpec    string
    StaleTimeout   time.Duration
    NotifyBuffer   int
}

type TeamCoordinator struct {
    ledger      TaskLedger
    agentMgr    *agent.AgentManager
    provider    agent.Provider
    config      TeamConfig
    notifyCh    chan Notification
    cancelFuncs map[string]context.CancelFunc
    mu          sync.Mutex
}

type Notification struct {
    Type    string  // "task_added", "task_completed", "task_failed", "task_blocked"
    TaskID  string
    AgentID string
    Message string
}

func (tc *TeamCoordinator) Run(ctx context.Context, goal string, sess *session.Session) (*TeamResult, error)
func (tc *TeamCoordinator) AddTask(ctx context.Context, task Task) error
func (tc *TeamCoordinator) Notify(n Notification)

type TeamResult struct {
    RootTaskID     string
    TasksCompleted int
    TasksFailed    int
    TasksCancelled int
    Summary        string        // LLM-synthesized summary of all results
    Duration       time.Duration
    WorkerResults  map[string][]Task  // agentID → tasks completed by that worker
}
```

### Execution flow

```
1. PLAN — LLM decomposes goal into tasks → ledger.AddTasks(...)
2. SPAWN WORKERS — WorkerCount goroutines, each runs:
   loop {
     task := ledger.ClaimNext(agentID, KindTeamTask)
     if task == nil:
       select { <-notifyCh: continue | <-timeout: check if all done | <-ctx.Done: return }
     if blockedByDeps(task): release, continue
     result := agentTool.Execute(ctx, task.Description)
     ledger.UpdateState(task.ID, completed/failed)
     tc.Notify("task_completed")
     // Worker may discover and add new tasks
   }
3. SYNTHESIS — collect results, LLM summarizes for user
```

### Dependency handling

Tasks declare `Metadata["depends_on"]` (comma-separated IDs). Workers that claim blocked tasks release them immediately. Completed-task notifications wake idle workers to retry blocked tasks.

### Notification channel (hybrid coordination)

Workers wait on `notifyCh` when no pending tasks exist. Notifications are broadcast on task completion/addition. No full message-passing — just wake signals.

### Relationship to existing components

| Component | Relationship |
|-----------|-------------|
| `AgentTool` (spawn) | Workers execute via `AgentTool.Execute` |
| `BackgroundManager` | `TeamCoordinator.Run` registers as a background task |
| `AgentOrchestrator` | Remains for programmatic DAG; teams use ledger-based claiming |
| `SidechainStore` | Workers optionally record sidechain entries |

### Slash commands

- `/team start <goal>` — start team session
- `/team status` — show workers + tasks + progress
- `/team add <task>` — manually add task
- `/team stop` — cancel all

### Config

```yaml
agent:
  team:
    enabled: true
    worker_count: 3
    worker_spec: "coder"
    planner_spec: ""
    stale_timeout: 5m
```

### Files Changed

| File | Change |
|------|--------|
| `internal/taskledger/team.go` | NEW — `TeamCoordinator`, worker loop |
| `internal/taskledger/team_planner.go` | NEW — LLM task decomposition |
| `internal/config/config.go` | Add `TeamConfig` |
| `internal/agent/agent_tool.go` | Expose `Execute` for programmatic use |
| `internal/channel/tui/commands.go` | `/team` commands |
| `internal/gateway/gateway.go` | Wire `TeamCoordinator` |

---

## Implementation Phases

| Phase | Features | Estimated Commits | Risk |
|-------|----------|-------------------|------|
| 1A | Unified Context Manager | 4-5 | Low — wraps existing pipeline |
| 1B | Speculative Executor | 3-4 | Low — additive, read-only tools only |
| 2A | Task Ledger | 4-5 | Medium — touches all execution paths |
| 2B | Agent Teams | 4-5 | Medium — new coordination pattern |

**Order**: 1A → 1B → 2A → 2B (each builds on the previous).

---

## Testing Strategy

- **1A**: Unit tests for `PipelineContextManager` (mock provider, verify layer ordering). Integration test: 413 retry with mock API.
- **1B**: Unit tests for `SpeculativeExecutor` (launch criteria, collect/cancel). Integration test: mock stream with tool_use blocks, verify speculative hits.
- **2A**: Unit tests for `SQLiteTaskLedger` (CRUD, ClaimNext atomicity, stale detection). Integration test: concurrent claims.
- **2B**: Unit tests for `TeamCoordinator` (worker lifecycle, dependency handling). Integration test: multi-worker task completion with notifications.
