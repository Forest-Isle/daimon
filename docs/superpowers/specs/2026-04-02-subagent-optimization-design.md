# IronClaw Subagent System Optimization Design

Based on claude-code's subagent architecture, adapted for Go ecosystem.

## Background

IronClaw's current subagent system (Phase 1 complete) provides basic agent spawning, tool scoping, and circuit breaker protection. Claude-code's subagent system offers several advanced capabilities that IronClaw lacks: Fork Agent (context inheritance), parallel agent scheduling, background async execution, prompt cache sharing, multi-execution backends, permission hierarchy, lifecycle hooks, sidechain execution recording, and per-agent MCP servers.

This design applies claude-code's core architectural patterns to IronClaw, adapted to leverage Go's goroutine/channel primitives.

## Approach: Refined Porting (方案 B)

Extract the most valuable architectural patterns from claude-code and adapt them for Go. Three iterative phases, each independently testable and deliverable. Breaking changes to existing AgentSpec/AgentTool API are permitted.

---

## Phase 1: Fork Agent + Parallel Scheduling + Context Inheritance

### 1.1 AgentSpec Extension

Extend the existing `AgentSpec` struct with new fields:

```go
type AgentSpec struct {
    // Existing fields retained
    Name          string
    Description   string
    SystemPrompt  string
    Model         string
    MaxTokens     int
    MaxIterations int
    Tools         []string
    Tags          []string
    Mode          string        // "simple" | "cognitive"
    Timeout       time.Duration
    RequiresApproval bool
    MaxRetries    int

    // New fields
    ExecutionMode   ExecutionMode   // "fork" | "spawn" | "background"
    PermissionMode  PermissionMode  // "bubble" | "accept_edits" | "bypass" | ""
    Hooks           AgentHooks      // Lifecycle hooks (Phase 3)
    MCPServers      []MCPConfig     // Per-agent MCP (Phase 3)
    Backend         BackendType     // "in_process" | "subprocess" | "docker" (Phase 3)
    InheritContext  bool            // Fork mode: inherit parent context
    MaxOutputTokens int             // Output token limit
}

type ExecutionMode string
const (
    ExecModeSpawn      ExecutionMode = "spawn"       // Default: independent creation
    ExecModeFork       ExecutionMode = "fork"        // Inherit parent context
    ExecModeBackground ExecutionMode = "background"  // Async fire-and-forget
)
```

**File**: `internal/agent/spec.go`

### 1.2 SubagentContext

New struct providing isolation and inheritance control for subagents:

```go
type SubagentContext struct {
    // Isolation layer
    ToolRegistry  *tool.Registry
    Permission    PermissionMode
    Cancel        context.CancelFunc
    AbortOnParent bool

    // Inheritance layer (read-only references)
    ParentMessages []Message
    SystemPrompt   string
    Memory         memory.Store
    Sessions       *session.Manager
    DB             *store.DB

    // Tracking
    AgentID       string
    ParentID      string
    Depth         int
    ChainID       string

    // Execution recording (Phase 2)
    Sidechain     *SidechainRecorder
}
```

**File**: `internal/agent/subagent_context.go` (new)

### 1.3 Fork Agent

Fork agents inherit the parent Runtime's complete session context (message history + system prompt), appending only the new directive.

```go
type ForkAgent struct {
    parent      *Runtime
    spec        *AgentSpec
    ctx         *SubagentContext
}

const MaxForkDepth = 3
```

Key behaviors:
- Message building: copy parent messages + append fork directive in `<fork-directive>` tags
- Recursion guard: reject if `Depth >= MaxForkDepth`
- Tool scoping: always exclude `agent_*` tools (prevent infinite recursion)
- Fallback: if parent Runtime unavailable, fall back to spawn mode

**File**: `internal/agent/fork.go` (new)

### 1.4 AgentOrchestrator

New component for parallel and DAG-based agent scheduling:

```go
type AgentOrchestrator struct {
    manager     *AgentManager
    provider    Provider
    maxParallel int  // Default: 4
}

type AgentTask struct {
    AgentName string
    Task      string
    Context   string
    DependsOn []string  // Task ID dependencies (DAG)
}

type AgentResult struct {
    AgentName  string
    Output     string
    Error      error
    Duration   time.Duration
    TokenUsage TokenUsage
}
```

Methods:
- `ExecuteParallel(ctx, parentRuntime, tasks)` — concurrent execution with `errgroup`, respecting `maxParallel` limit
- `ExecuteDAG(ctx, parentRuntime, tasks)` — topological sort then layer-by-layer parallel execution
- Individual agent failures don't abort the entire batch

**File**: `internal/agent/orchestrator.go` (new)

### 1.5 Refactored AgentTool.Execute()

Dispatch based on `ExecutionMode`:

```go
func (a *AgentTool) Execute(ctx context.Context, input []byte) (tool.Result, error) {
    // 1. Circuit breaker check (retained)
    // 2. Parse input
    // 3. Dispatch by execution mode:
    switch a.spec.ExecutionMode {
    case ExecModeFork:
        return a.executeFork(ctx, req)
    case ExecModeBackground:
        return a.executeBackground(ctx, req)
    default:
        return a.executeSpawn(ctx, req)
    }
}
```

Parent Runtime passed via `context.Context` using `RuntimeFromContext(ctx)`.

**File**: `internal/agent/agent_tool.go` (modified)

### 1.6 Files Changed/Created (Phase 1)

| File | Action | Description |
|------|--------|-------------|
| `internal/agent/spec.go` | Modified | Add ExecutionMode, PermissionMode, new fields |
| `internal/agent/subagent_context.go` | New | SubagentContext struct and builder |
| `internal/agent/fork.go` | New | ForkAgent implementation |
| `internal/agent/orchestrator.go` | New | AgentOrchestrator with parallel/DAG |
| `internal/agent/agent_tool.go` | Modified | Dispatch by ExecutionMode |
| `internal/agent/agent_manager.go` | Modified | Support new spec fields in loading |
| `internal/agent/runtime.go` | Modified | Expose GetMessages(), GetSystemPrompt(), AgentID(), Depth(), ChainID(); add RuntimeToContext/RuntimeFromContext |

---

## Phase 2: Background Async + Prompt Cache + Sidechain

### 2.1 BackgroundManager

Manages all background agents with fire-and-forget execution and notification channels:

```go
type BackgroundManager struct {
    mu       sync.RWMutex
    agents   map[string]*BackgroundAgent
    notifyCh chan AgentStatus
}

type BackgroundAgent struct {
    spec     *AgentSpec
    subCtx   *SubagentContext
    resultCh chan *AgentResult
    statusCh chan AgentStatus
}

type AgentStatus struct {
    AgentID   string
    State     AgentState  // Running | Completed | Failed | Cancelled
    Progress  string
    UpdatedAt time.Time
}
```

Key behaviors:
- `Spawn()` — launch agent in background goroutine, return agent ID immediately
- `GetResult(agentID)` — non-blocking result query
- Permission Bubble: background agents send permission requests to parent's channel
- Notification: aggregated status channel for parent to monitor

**File**: `internal/agent/background.go` (new)

### 2.2 PromptCache

Optimize system prompt construction and message prefix reuse:

```go
type PromptCache struct {
    mu    sync.RWMutex
    cache map[string]*CachedPrompt
}

type CachedPrompt struct {
    SystemPrompt string
    Hash         string
    CreatedAt    time.Time
    HitCount     int64
}

type ForkMessagePrefix struct {
    Messages     []Message
    SystemPrompt string
    Hash         string  // Prefix hash for API-level cache key hint
}
```

Key behaviors:
- `GetOrBuild(spec, builder)` — deduplicate identical system prompts across agents
- `GetForkPrefix(parentRuntime)` — reuse parent's message prefix for fork children
- Cache key derived from spec config (name, model, system prompt hash)

**File**: `internal/agent/prompt_cache.go` (new)

### 2.3 Sidechain Execution Recording

Independent execution history for subagents, separate from main conversation:

```go
type SidechainRecorder struct {
    agentID   string
    parentID  string
    chainID   string
    store     SidechainStore
    messages  []SidechainEntry
    mu        sync.Mutex
}

type SidechainEntry struct {
    ID, AgentID, ParentID string
    Timestamp             time.Time
    Type                  string  // "message" | "tool_call" | "tool_result" | "status"
    Content               string
    Metadata              map[string]string
}

type SidechainStore interface {
    Append(entry SidechainEntry) error
    GetByAgent(agentID string) ([]SidechainEntry, error)
    GetByChain(chainID string) ([]SidechainEntry, error)
}
```

Two implementations:
- `SQLiteSidechainStore` — uses existing store.DB, suitable for production
- `FileSidechainStore` — lightweight file-based, suitable for debugging (`~/.ironclaw/sidechains/`)

Recovery: `RecoverFromSidechain(store, agentID)` reconstructs message history from sidechain entries.

**File**: `internal/agent/sidechain.go` (new)

### 2.4 Files Changed/Created (Phase 2)

| File | Action | Description |
|------|--------|-------------|
| `internal/agent/background.go` | New | BackgroundManager, BackgroundAgent |
| `internal/agent/prompt_cache.go` | New | PromptCache, ForkMessagePrefix |
| `internal/agent/sidechain.go` | New | SidechainRecorder, SidechainStore implementations |
| `internal/agent/agent_tool.go` | Modified | Wire executeBackground() to BackgroundManager |
| `internal/agent/runtime.go` | Modified | Integrate PromptCache in system prompt building |
| `internal/store/migrations/` | Modified | Add sidechain_entries table |

---

## Phase 3: Multi-Backend + Permission Hierarchy + Lifecycle Hooks + Per-Agent MCP

### 3.1 ExecutionBackend Interface

Abstraction over execution environments:

```go
type ExecutionBackend interface {
    Execute(ctx context.Context, config BackendConfig) (<-chan *AgentResult, error)
    Available() bool
    Cleanup() error
}

type BackendConfig struct {
    Spec          *AgentSpec
    SubCtx        *SubagentContext
    Task          string
    ParentRuntime *Runtime
    EnvVars       map[string]string
}
```

Three implementations:
- `InProcessBackend` — goroutine execution (default, zero overhead)
- `SubprocessBackend` — `os/exec` child process with inherited env vars
- `DockerBackend` — container execution for full isolation

`SelectBackend(spec)` factory selects backend based on spec configuration.

**File**: `internal/agent/backend.go` (new)

### 3.2 Permission Hierarchy

Three-level permission model:

```go
type PermissionMode string
const (
    PermModeDefault     PermissionMode = ""
    PermModeBubble      PermissionMode = "bubble"
    PermModeAcceptEdits PermissionMode = "accept_edits"
    PermModeBypass      PermissionMode = "bypass"
)

type PermissionEvaluator struct {
    mode     PermissionMode
    parentCh chan<- PermissionRequest
    rules    []PermissionRule
}
```

Evaluation logic:
- `bypass` — allow everything (use only for trusted, read-only agents like planners)
- `accept_edits` — auto-approve read/write, bubble dangerous operations (dangerous = `rm -rf`, `chmod 777`, `kill`, network-modifying commands; maintained as a blocklist in `policy.go`)
- `bubble` — send all permission requests to parent Runtime via channel; parent responds with allow/deny
- default (empty string) — evaluate static rules from existing `tool/policy.go` permission checks

**File**: `internal/agent/permission.go` (new)

### 3.3 Lifecycle Hooks

```go
type AgentHooks struct {
    OnStart    []HookFunc
    OnComplete []HookFunc
    OnError    []HookFunc
    OnTimeout  []HookFunc
    OnToolCall []HookFunc
}

type HookFunc func(ctx context.Context, hctx *HookContext) error
```

Hook types in YAML config: `log`, `exec`, `webhook`. Hook failures log warnings but don't block agent execution.

**File**: `internal/agent/hooks.go` (new)

### 3.4 Per-Agent MCP

```go
type AgentMCPManager struct {
    parentClients []*mcp.Client
    agentClients  map[string][]*mcp.Client
}
```

`InitializeForAgent(spec)` merges parent MCP clients with agent-specific ones. `CleanupForAgent(agentName)` closes agent-specific clients.

**File**: `internal/agent/agent_mcp.go` (new)

### 3.5 Files Changed/Created (Phase 3)

| File | Action | Description |
|------|--------|-------------|
| `internal/agent/backend.go` | New | ExecutionBackend interface + 3 implementations |
| `internal/agent/permission.go` | New | PermissionEvaluator, PermissionMode |
| `internal/agent/hooks.go` | New | AgentHooks, HookRunner |
| `internal/agent/agent_mcp.go` | New | AgentMCPManager |
| `internal/agent/agent_tool.go` | Modified | Wire backends, permissions, hooks |
| `internal/agent/runtime.go` | Modified | Integrate hooks into agent lifecycle |
| `internal/agent/spec.go` | Modified | Add Hooks, MCPServers YAML parsing |

---

## Testing Strategy

### Phase 1 Tests
- Fork Agent: context inheritance correctness, recursion depth guard, fallback to spawn
- Orchestrator: parallel execution, DAG ordering, partial failure handling
- SubagentContext: isolation (tool scoping, cancel propagation)

### Phase 2 Tests
- BackgroundManager: spawn/query/cancel lifecycle, permission bubble
- PromptCache: hit/miss, concurrent access, cache key correctness
- Sidechain: record/recover round-trip, SQLite and file store

### Phase 3 Tests
- Backends: InProcess/Subprocess/Docker execution, cleanup
- Permissions: bubble/accept_edits/bypass behavior, dangerous op detection
- Hooks: execution order, failure isolation
- MCP: initialize/cleanup per-agent servers

---

## YAML Configuration Example (Complete)

```yaml
# ~/.IronClaw/agents/code-writer.yaml
name: code-writer
description: Writes and tests code changes
system_prompt: |
  You are a code implementation expert...
model: claude-sonnet-4-20250514
max_iterations: 10
timeout: 300s
execution_mode: fork           # inherit parent context
permission_mode: accept_edits  # auto-approve file writes
backend: in_process
inherit_context: true
max_output_tokens: 16000

tools:
  - bash
  - file_read
  - file_write
  - file_edit

mcp_servers:
  - name: workspace
    command: mcp-filesystem
    args: ["/home/user/project"]

hooks:
  on_start:
    - type: log
      message: "Code writer starting: {{.Task}}"
  on_complete:
    - type: exec
      command: "echo 'done' >> /tmp/agent-audit.log"
  on_error:
    - type: webhook
      url: "https://hooks.slack.com/..."

tags: [code, implementation]
```

---

## Migration Notes

- Existing `agents/*.yaml` files will continue to work; new fields default to current behavior (`execution_mode: spawn`, `permission_mode: ""`, `backend: in_process`)
- `AgentTool.Execute()` retains existing spawn behavior when `ExecutionMode` is empty or "spawn"
- Circuit breaker pattern retained and applied to all execution modes
- `buildScopedRegistry()` logic unchanged; `agent_*` exclusion applies to all modes

## Architecture Diagram

```
┌──────────────────────────────────────────────────────┐
│  AgentOrchestrator (Phase 1)                         │
│  - Parallel / DAG scheduling                         │
│  - BackgroundManager integration (Phase 2)           │
└────────────┬─────────────────────────────────────────┘
             │
     ┌───────┼───────┐
     ▼       ▼       ▼
┌─────────┐┌──────┐┌───────────┐
│ForkAgent││Spawn ││Background │
│(ctx inh)││Agent ││Agent      │
└────┬────┘└──┬───┘└────┬──────┘
     │        │         │
     ▼        ▼         ▼
┌──────────────────────────────────────────────────────┐
│  ExecutionBackend (Phase 3)                          │
│  InProcess | Subprocess | Docker                     │
└────────────┬─────────────────────────────────────────┘
             │
┌────────────┴─────────────────────────────────────────┐
│  SubagentContext                                     │
│  ┌──────────────┐  ┌──────────────┐                  │
│  │ToolRegistry  │  │PermEvaluator │                  │
│  │(scoped)      │  │(Phase 3)     │                  │
│  └──────────────┘  └──────────────┘                  │
│  ┌──────────────┐  ┌──────────────┐                  │
│  │PromptCache   │  │Sidechain     │                  │
│  │(Phase 2)     │  │Recorder(Ph2) │                  │
│  └──────────────┘  └──────────────┘                  │
│  ┌──────────────┐  ┌──────────────┐                  │
│  │HookRunner    │  │AgentMCP      │                  │
│  │(Phase 3)     │  │Manager(Ph3)  │                  │
│  └──────────────┘  └──────────────┘                  │
└──────────────────────────────────────────────────────┘
```
