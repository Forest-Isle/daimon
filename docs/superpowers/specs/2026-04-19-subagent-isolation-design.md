# Sub-Agent Isolation & Orchestration Design

## Problem

IronClaw's current sub-agent system has two execution paths with limited isolation:

1. **AgentTool** (`agent_tool.go`) — Creates a temporary Runtime per invocation, but uses a fixed session key (`agent_<name>`), meaning repeated calls to the same agent share context. Each `AgentTool` holds 7+ dependencies and manually constructs scoped registries, duplicating logic across spawn/fork/background modes.

2. **TeamCoordinator** (`taskledger/team.go`) — Goroutine-level worker pool backed by a TaskLedger (SQLite). Workers share the same Provider. The executor (`gateway.executeTeamTask`) is a **single `provider.Complete` call** — no tool access, no multi-turn reasoning, no session.

**Gaps vs. Claude Code sub-agents:**

| Capability | Claude Code | IronClaw (current) |
|---|---|---|
| Independent context window | Yes — each subagent has its own | No — same-name agents share session |
| Full agent loop per sub-agent | Yes — tool calls, multi-turn | No — TeamCoordinator is single-shot LLM |
| Model routing per sub-agent | Yes — per-agent model selection | Partial — AgentSpec.Model exists but TeamCoordinator ignores it |
| Structured result aggregation | Yes — summary returned to parent | No — raw text output only |
| Declarative .md definition | Yes — `.claude/agents/*.md` | No — YAML only |

## Decisions

| Question | Choice |
|---|---|
| Scope | Unified SubAgentManager for both AgentTool and TeamCoordinator |
| Model routing | Same Provider, different Model (via `CompletionRequest.Model`) |
| Result aggregation | Template-based extraction with LLM fallback |
| Failure strategy | Configurable per-agent (`best_effort` default, `fail_fast` option) |
| Agent definition format | Markdown (YAML frontmatter + body), coexists with existing YAML |

## Architecture Overview

```
┌──────────────────────────────────────────────┐
│                   Gateway                     │
│                                               │
│  ┌─────────────┐     ┌──────────────────┐    │
│  │  AgentTool   │────▶│ SubAgentManager  │    │
│  └─────────────┘     │                  │    │
│                      │  .Spawn()        │    │
│  ┌─────────────┐     │  .SpawnParallel()│    │
│  │TeamCoord.   │────▶│                  │    │
│  │  executor   │     └──────┬───────────┘    │
│  └─────────────┘            │                │
│                             ▼                │
│                    ┌────────────────┐         │
│                    │  Per-invocation │         │
│                    │  ┌──────────┐  │         │
│                    │  │ Session  │  │  unique  │
│                    │  │ (unique) │  │  per     │
│                    │  ├──────────┤  │  call    │
│                    │  │ Scoped   │  │         │
│                    │  │ Registry │  │         │
│                    │  ├──────────┤  │         │
│                    │  │ Model    │  │         │
│                    │  │ Override │  │         │
│                    │  ├──────────┤  │         │
│                    │  │ Runtime  │  │         │
│                    │  │  (loop)  │  │         │
│                    │  └──────────┘  │         │
│                    └────────┬───────┘         │
│                             │                │
│                             ▼                │
│                    ┌────────────────┐         │
│                    │SubAgentResult  │         │
│                    │ .Summary       │         │
│                    │ .Artifacts     │         │
│                    │ .Status        │         │
│                    └────────────────┘         │
└──────────────────────────────────────────────┘
```

## Component Design

### 1. SubAgentManager (`internal/agent/subagent.go`)

Central manager for sub-agent lifecycle. Both `AgentTool` and `TeamCoordinator` delegate to it.

```go
type SubAgentManager struct {
    provider   Provider
    sessions   *session.Manager
    db         *store.DB
    memStore   memory.Store
    tools      *tool.Registry
    cfg        config.AgentConfig
    llmCfg     config.LLMConfig
    bgManager  *BackgroundManager
    agentMCP   *AgentMCPManager
}

type SpawnRequest struct {
    Spec        *AgentSpec
    Task        string
    TaskContext string
    ParentID    string
    ParentDepth int
    ChainID     string
}

func (m *SubAgentManager) Spawn(ctx context.Context, req SpawnRequest) (*SubAgentResult, error)
func (m *SubAgentManager) SpawnParallel(ctx context.Context, reqs []SpawnRequest, strategy FailureStrategy) ([]*SubAgentResult, error)
```

**`Spawn` internal flow:**

1. Generate unique session ID: `subagent_<name>_<uuid8>`
2. Build scoped tool registry from parent registry + spec tool whitelist
3. Build sub-agent config with model/max_tokens/system_prompt overrides
4. Append `subagentOutputInstruction` to system prompt for structured output
5. Create Runtime with scoped tools, run `HandleMessage` via `captureChannel`
6. Set lineage tracking (agentID, parentID, depth, chainID)
7. On completion, delete ephemeral session from `session.Manager`
8. Extract structured result (template parse → LLM fallback)
9. Return `SubAgentResult`

**Execution mode dispatch** (inside `Spawn`):

- `spawn` (default): clean session, no parent context. Synchronous — blocks until agent completes.
- `fork`: inherit parent messages via `SubagentContext`, create child session. Synchronous.
- `background`: wraps spawn logic in `BackgroundManager.Spawn`, returns immediately with `SubAgentResult{Status: StatusBackground, Summary: "Background agent started: <agentID>"}`. Caller queries `BackgroundManager` for eventual result. `StatusBackground` is a new status constant for this case.

### 2. Context Isolation

**Problem:** Current `AgentTool` uses fixed `channelID: "agent_<name>"`, so multiple calls to the same agent pollute each other's context.

**Solution:** Each `Spawn()` generates a unique session key:

```go
sessionID := fmt.Sprintf("subagent_%s_%s", req.Spec.Name, uuid.New().String()[:8])
```

**Session lifecycle:**
- Created automatically by `session.Manager.Get()` on first access
- Lives only during `Spawn()` execution
- Deleted after `Spawn()` returns via new `session.Manager.Delete()` method
- Optional `persist_session` flag on `AgentSpec` for future `/resume` support

**New method on `session.Manager`:**

```go
func (m *Manager) Delete(ctx context.Context, channel, channelID string) error
```

Removes session from in-memory cache and optionally from SQLite (configurable).

### 3. Structured Result Aggregation (`internal/agent/subagent_result.go`)

```go
type SubAgentResult struct {
    AgentName  string         `json:"agent_name"`
    Status     SubAgentStatus `json:"status"`
    Summary    string         `json:"summary"`
    Output     string         `json:"output"`
    Artifacts  []string       `json:"artifacts"`
    Duration   time.Duration  `json:"duration"`
    TokensUsed int            `json:"tokens_used"`
    Error      string         `json:"error,omitempty"`
}

type SubAgentStatus string
const (
    StatusSuccess    SubAgentStatus = "success"
    StatusError      SubAgentStatus = "error"
    StatusTimeout    SubAgentStatus = "timeout"
    StatusBackground SubAgentStatus = "background"
)
```

**Two-phase extraction:**

**Phase 1 — Template extraction.** A structured output instruction is appended to the sub-agent's system prompt:

```
When you have completed the task, output your final response in this format:

<result>
<status>success|error</status>
<summary>One paragraph summary of what was accomplished</summary>
<artifacts>Comma-separated list of file paths, URLs, or key outputs (if any)</artifacts>
</result>
```

`extractStructuredResult(raw string) *SubAgentResult` parses this XML block from the agent's final output.

**Phase 2 — LLM fallback.** When the agent doesn't follow the format, a lightweight LLM call (max 256 tokens) summarizes the raw output into the same structure.

**Fallback chain:** template parse → LLM summarize → truncated raw output (if LLM also fails).

**Parent-facing format:** `formatResultForParent(r *SubAgentResult) string` produces a concise summary for the parent agent's tool result, not the full raw output.

### 4. Parallel Execution & Failure Strategy

```go
type FailureStrategy string
const (
    StrategyBestEffort FailureStrategy = "best_effort"
    StrategyFailFast   FailureStrategy = "fail_fast"
)
```

**`SpawnParallel` behavior:**

- `best_effort` (default): All sub-agents run to completion. Failures are collected. Parent receives mixed success/error results.
- `fail_fast`: First failure cancels all sibling sub-agents via shared `context.WithCancel`. Returns partial results + error.

**New field on `AgentSpec`:**

```go
FailureStrategy FailureStrategy `yaml:"failure_strategy"`
```

Default: `best_effort`. Validated in `AgentSpec.Validate()`.

**TeamCoordinator integration:** The coordinator's worker pool and dependency management remain unchanged. Only the executor function changes from single `provider.Complete` to `SubAgentManager.Spawn()`. Each claimed task becomes a full agent loop with tools.

### 5. Markdown Agent Spec Format

**File format:** YAML frontmatter + Markdown body (system prompt), matching Claude Code's `.claude/agents/*.md`.

**Example `~/.IronClaw/agents/code-reviewer.md`:**

```markdown
---
name: "code-reviewer"
description: "Review code changes for quality, security, and best practices."
model: claude-3-5-haiku-20241022
max_iterations: 5
tools:
  - bash
  - file
timeout: "180s"
execution_mode: spawn
permission_mode: accept_edits
failure_strategy: best_effort
tags:
  - review
---

You are an expert code reviewer. Focus on:

1. **Correctness** — logic errors, edge cases
2. **Security** — injection, auth bypass
3. **Performance** — unnecessary allocations
4. **Maintainability** — naming, structure, tests

Be specific. Reference line numbers. Suggest fixes, not just problems.
```

**Loader changes in `AgentManager.LoadDir`:**

- `.yaml`/`.yml` → existing `loadAgentSpec` (unchanged)
- `.md` → new `loadMarkdownAgentSpec` (splits frontmatter from body, body becomes `SystemPrompt`)
- If both `foo.yaml` and `foo.md` exist, `.md` takes precedence

**`loadMarkdownAgentSpec` implementation:**

Uses `splitFrontmatter(content) (yaml, body, error)` to separate `---` delimited YAML from the Markdown body. YAML is unmarshalled into `AgentSpec`, body is assigned to `spec.SystemPrompt`.

**Backward compatibility:** Fully backward compatible. Existing YAML specs continue to work. The `.md` format is additive.

### 6. AgentTool Simplification

**Before:** `AgentTool` holds 7+ dependencies (provider, sessions, db, memStore, tools, cfg, llmCfg), manually builds Runtime in three separate methods (spawn/fork/background).

**After:** `AgentTool` holds only `spec`, `manager *SubAgentManager`, and `breaker`. All execution delegated to `SubAgentManager.Spawn()`.

```go
type AgentTool struct {
    spec    *AgentSpec
    manager *SubAgentManager
    breaker *CircuitBreaker
}

func (a *AgentTool) Execute(ctx context.Context, input []byte) (tool.Result, error) {
    // parse input, apply timeout
    result, err := a.manager.Spawn(ctx, SpawnRequest{...})
    // handle errors, format result
    return tool.Result{Output: formatResultForParent(result)}, nil
}
```

**AgentManager changes:** `RegisterAll` passes `SubAgentManager` instead of 7+ raw dependencies. `NewAgentTool(spec, manager)` replaces the current 8-parameter constructor.

### 7. Gateway Wiring

Initialization order in `gateway.go`:

```
initDatabase → initToolsAndHooks → initAgentRuntime → initMemorySystem
→ initCognitiveAgent → initKnowledgeSystem → initSkillManager → initMultiAgent
→ NEW: create SubAgentManager
→ wire SubAgentManager into AgentManager
→ wire SubAgentManager into TeamCoordinator executor
→ task ledger, scheduler, MCP, approval
```

**New gateway field:**

```go
type Gateway struct {
    // ...existing fields...
    subAgentMgr *agent.SubAgentManager
}
```

**Wiring code:**

```go
gw.subAgentMgr = agent.NewSubAgentManager(
    gw.provider, gw.sessions, gw.db,
    gw.memStore, gw.tools,
    gw.cfg.Agent, gw.cfg.LLM,
)

if gw.agentMgr != nil {
    gw.agentMgr.SetSubAgentManager(gw.subAgentMgr)
}

if cfg.Agent.Team.Enabled {
    tc.SetExecutor(func(ctx context.Context, task taskledger.Task) (string, error) {
        result, err := gw.subAgentMgr.Spawn(ctx, agent.SpawnRequest{
            Spec: buildTeamWorkerSpec(task, gw.cfg),
            Task: task.Description,
        })
        if err != nil { return "", err }
        return result.Summary, nil
    })
}
```

## Files Changed

| File | Action | Description |
|---|---|---|
| `internal/agent/subagent.go` | Create | SubAgentManager, Spawn, SpawnParallel, buildSubConfig, buildScopedRegistry (moved from agent_tool.go), captureChannel + captureUpdater (moved from agent_tool.go) |
| `internal/agent/subagent_result.go` | Create | SubAgentResult, SubAgentStatus, extractStructuredResult, summarizeWithLLM, formatResultForParent |
| `internal/agent/agent_tool.go` | Modify | Simplify to delegate to SubAgentManager; remove executeAgentCore, executeFork, buildScopedRegistry, captureChannel |
| `internal/agent/agent_manager.go` | Modify | Add SetSubAgentManager; simplify RegisterAll; add loadMarkdownAgentSpec + splitFrontmatter |
| `internal/agent/spec.go` | Modify | Add FailureStrategy field + validation |
| `internal/session/manager.go` | Modify | Add Delete method for ephemeral session cleanup |
| `internal/gateway/gateway.go` | Modify | Add subAgentMgr field; wire into AgentManager + TeamCoordinator |
| `internal/taskledger/team.go` | No change | Worker pool logic unchanged; only the executor closure changes |

## Testing Strategy

- **Unit tests for SubAgentManager.Spawn:** Mock Provider, verify independent session creation, scoped tool filtering, model override, result extraction.
- **Unit tests for extractStructuredResult:** Valid XML, malformed XML, missing block, partial fields.
- **Unit tests for loadMarkdownAgentSpec:** Valid .md, missing frontmatter, empty body, env var expansion.
- **Unit tests for SpawnParallel:** Both strategies — best_effort collects all, fail_fast cancels siblings.
- **Unit tests for session.Manager.Delete:** Verify in-memory + DB cleanup.
- **Integration test:** AgentTool → SubAgentManager → Runtime → captureChannel → SubAgentResult pipeline.
- **Integration test:** TeamCoordinator with upgraded executor running multi-turn tool-using tasks.

## Future Extensions (Out of Scope)

- **Multi-provider routing** (B option): ProviderRegistry supporting Claude + OpenAI + local backends simultaneously. Current design's `SubAgentManager` interface would serve as the adaptation layer.
- **Process-level isolation** (approach 3): Each sub-agent as a separate OS process or Docker container. Current `Backend` abstraction already supports this.
- **`persist_session` flag:** Allow sub-agent sessions to survive for `/resume` workflows.
- **Per-agent MCP servers:** Wire `AgentMCPManager.InitializeForAgent` into `SubAgentManager.Spawn`.
