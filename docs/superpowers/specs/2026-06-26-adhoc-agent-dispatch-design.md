# Ad-hoc Agent Dispatch (`agent_dispatch`)

Date: 2026-06-26

## Problem

The agent can only delegate to **pre-registered** sub-agents: each `AgentSpec`
(from config or `~/.daimon/agents/*.md`) becomes an `agent_<name>` tool, and the
`workflow` tool resolves steps via `agentMgr.Get(step.Agent)`. With zero agents
registered there is no delegation affordance at all — the only multi-agent tool
the model sees is `workflow`, whose `type=agent` steps require a registered
agent, so every call fails (`workflow/spec.go:145` "agent step requires agent").

Observed failure (trace `~/.daimon/traces/events.jsonl`, 2026-06-26): asked to
demo sub-agents, the model claimed "I delegate via workflow", then submitted 8
workflow specs with no valid agent → 8 rejections → it looped guessing names
until the iteration cap, never falling back to its own tools.

Claude Code (and the Claude Agent SDK) avoid this: the orchestrator dispatches a
subagent **ad-hoc** by passing `{description, prompt, tools?, model?}` inline at
dispatch time — no pre-registration — with a built-in `general-purpose` agent
always available. (Confirmed against current Claude Code / Agent SDK docs.)

## Goal

Add an always-available `agent_dispatch` tool that lets the main agent spawn an
**ephemeral, isolated, tool-scoped** sub-agent from an inline definition — no
pre-registered spec required. This is the `general-purpose`/`Task` equivalent.

Non-goal for this spec (deliberate follow-up): inline agent steps inside the
`workflow` tool (Piece ②). This spec is Piece ① only.

## Scope Decisions (resolved)

- **Ephemeral, not persistent.** Dispatch builds a throwaway `AgentSpec` and
  runs it through the existing `SubAgentManager.Spawn`; nothing is written to the
  durable agent roster. (This is the safe half of "self-registration" — the
  unsafe half, mutating the persistent roster at runtime, is explicitly NOT
  built.)
- **Tool name `agent_dispatch`.** The `agent_` prefix means
  `buildScopedRegistryStandalone` (which already drops `agent_*` from a
  sub-agent's scoped registry) automatically excludes it from sub-agents — so by
  default a dispatched worker cannot re-dispatch. v1 nesting depth is therefore
  1 (main dispatches workers; workers do not). A defensive depth cap is still
  enforced (below) for the registered-agent paths that can nest.
- **Default toolset = read-only safe set** when `tools` is omitted:
  `[file_read, grep_code, find_symbol]`. IronClaw fences tools by posture, so an
  ad-hoc worker does not silently inherit write/exec tools; the caller must list
  them explicitly to grant more.
- **Approval/trust via the existing chain.** `agent_dispatch` reports
  `RequiresApproval() = true` like a destructive-ish action, so it flows through
  the permission → trust interceptor chain already in place (the trust system
  escalates autonomy as the user approves).

## Architecture

### Reused as-is (no change)

- `SubAgentManager.Spawn(SpawnRequest{Spec, Task, ParentSessionID, ParentDepth})`
  — ephemeral session (`subagent_<name>_<uuid>`, `Channel:"subagent"`), scoped
  tool registry, isolation, episode/linear execution, session cleanup.
- `buildScopedRegistryStandalone(parent, whitelist)` — allowlist scoping; drops
  `agent_*` (recursion guard).
- Activity forwarding (shipped): the dispatched worker's tool steps surface in
  the parent TUI transcript nested under the `agent_dispatch` step (`⤷`), via
  `ParentSessionID` + the reporter parent-walk.
- Permission/trust interceptor chain; circuit breaker pattern (as in `AgentTool`).

### New: `agent_dispatch` tool

`internal/agent/dispatch_tool.go` — a `tool.Tool` that builds an ephemeral spec
and delegates to `SubAgentManager.Spawn`.

```go
type dispatchToolInput struct {
    Description string   `json:"description"`           // when/what this worker is for
    Prompt      string   `json:"prompt,omitempty"`      // worker system prompt (persona/instructions)
    Task        string   `json:"task"`                  // the concrete task (required)
    Tools       []string `json:"tools,omitempty"`       // allowlist; empty = read-only default set
    Model       string   `json:"model,omitempty"`       // optional model override
    Agent       string   `json:"agent,omitempty"`       // optional: reference a registered spec instead of inline
}

type DispatchTool struct {
    manager *SubAgentManager
    agents  *AgentManager        // to resolve an optional registered `agent` reference
    breaker *mind.CircuitBreaker
}

func (t *DispatchTool) Name() string { return "agent_dispatch" }
func (t *DispatchTool) RequiresApproval() bool { return true }
```

`Execute`:
1. Parse input; `task` required (else error result).
2. Resolve the spec:
   - If `Agent != ""` and `agents.Get(Agent)` hits → use that registered spec.
   - Else build an ephemeral spec:
     ```go
     spec := &AgentSpec{
         Name:          "dispatch",            // Validate() fills defaults
         Description:   in.Description,         // required by Validate; fall back to a constant if empty
         SystemPrompt:  in.Prompt,
         Tools:         defaultIfEmpty(in.Tools, []string{"file_read", "grep_code", "find_symbol"}),
         Model:         in.Model,
         MaxIterations: DefaultMaxIterations,   // 5
     }
     ```
3. `Spawn(ctx, SpawnRequest{Spec: spec, Task: in.Task, ParentID: AgentFromContext(ctx).AgentID(), ParentDepth: <from SubagentContext>, ParentSessionID: tool.SessionIDFromContext(ctx)})`.
4. Return `tool.Result{Output: result.Summary}` (or `{Error: ...}` on failure), mirroring `AgentTool.Execute` (circuit breaker, error mapping).

Description (for the model): "Dispatch an ephemeral sub-agent to do a focused
task. Provide `task` and a `prompt` describing the worker; optionally `tools`
(default: read-only file_read/grep_code/find_symbol), `model`, or `agent` to use
a registered agent. The worker runs isolated and returns its findings."

### New: depth-cap guardrail

`SubAgentManager.Spawn` currently computes `Depth: ParentDepth+1` but never
rejects deep nesting. Add an enforced cap (default 5, matching Claude Code):

```go
const MaxSubAgentDepth = 5
// in Spawn, before running:
if req.ParentDepth+1 > MaxSubAgentDepth {
    return nil, fmt.Errorf("sub-agent depth limit (%d) exceeded", MaxSubAgentDepth)
}
```

This bounds the registered-agent nesting paths (which CAN recurse). The
`agent_dispatch` tool is already depth-1-bounded by the `agent_` prefix
exclusion, but the cap is a defense-in-depth backstop.

### Registration

`internal/gateway/subsystem_multiagent.go` — when `multi_agent` is enabled and
`SubAgentMgr`/`AgentMgr` exist, register `agent_dispatch` into the tool registry
(next to where `WorkflowTool` is registered, gateway.go:198), **unconditionally
on agent count** — it needs no registered agents, that is its purpose.

## Components

| Unit | Responsibility | Depends on |
| --- | --- | --- |
| `DispatchTool` | parse inline def, build ephemeral spec, Spawn, map result | `SubAgentManager`, `AgentManager`, `mind.CircuitBreaker` |
| ephemeral `AgentSpec` build | defaults (tools, name, iterations) + Validate | `AgentSpec.Validate` |
| depth cap in `Spawn` | reject `ParentDepth+1 > MaxSubAgentDepth` | — |
| registration | always register `agent_dispatch` when multi_agent on | tool `Registry` |

## Error Handling

- `task` empty → `tool.Result{Error: "task is required"}` (no spawn).
- Spawn error / sub-agent error → mapped to `tool.Result{Error: ...}` (as
  `AgentTool` does); circuit breaker opens after repeated failures.
- Depth cap exceeded → `Spawn` returns an error → surfaced as the dispatch
  result's error (the model sees a clear message, not a silent loop).
- Unknown `tools` names → `buildScopedRegistryStandalone` silently drops them
  (allowlist semantics); if the resulting set is empty the worker simply has no
  tools (acceptable; its prompt can still reason/report).

## Testing

- `DispatchTool.Execute` with inline def (no registered agents) → spawns, returns
  the worker summary (use a fake/stub `SubAgentManager` or the existing
  sub-agent test harness).
- `tools` omitted → ephemeral spec gets the read-only default set.
- `agent` referencing a registered spec → uses it instead of the inline fields.
- empty `task` → error, no spawn.
- depth cap: `Spawn` with `ParentDepth = MaxSubAgentDepth` → error.
- registration: `agent_dispatch` present in the registry with zero configured
  agents (when multi_agent on).
- (already covered) activity forwarding renders the worker's steps nested — no
  new test needed here.

## Files Touched

- Create: `internal/agent/dispatch_tool.go` (+ `dispatch_tool_test.go`)
- Modify: `internal/agent/subagent.go` (depth cap + `MaxSubAgentDepth` const)
- Modify: `internal/gateway/subsystem_multiagent.go` (register `agent_dispatch`)
- Modify: `internal/agent/subagent_test.go` or new test (depth cap)

## Out of Scope (follow-ups)

- **Piece ②**: inline agent steps inside the `workflow` tool (extend the workflow
  step schema + executor + `spec.go:145` validation to accept an inline
  definition). Separate spec.
- Multi-level ad-hoc nesting (letting a dispatched worker re-dispatch) — would
  require granting `agent_dispatch` into sub-scopes and relying on the depth cap;
  intentionally deferred (v1 is depth-1).
- A hot-reload watcher on `~/.daimon/agents/` (separate concern).
- Persistent runtime registration of agents into the durable roster (explicitly
  rejected as unsafe).
