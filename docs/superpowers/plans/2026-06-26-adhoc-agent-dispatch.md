# Ad-hoc Agent Dispatch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an always-available `agent_dispatch` tool that spawns an ephemeral, isolated, tool-scoped sub-agent from an inline definition — no pre-registered agent spec required.

**Architecture:** A new `tool.Tool` (`DispatchTool`) builds a throwaway `AgentSpec` from inline JSON input and delegates to the existing `SubAgentManager.Spawn` (which already does session isolation, tool scoping, depth tracking, and — shipped — activity forwarding to the parent transcript). A defensive depth cap is added to `Spawn`. The tool is registered whenever the multi-agent runtime is up, regardless of how many agents are configured.

**Tech Stack:** Go 1.25.11, existing `internal/agent` sub-agent runtime, `internal/tool` registry, `internal/mind` circuit breaker.

## Global Constraints

- Go 1.25.11; standard `testing`; tests in-package (not `_test` package).
- Work on a feature branch (`feat/adhoc-agent-dispatch`), not `main`.
- `context.Context` is the first parameter; never ignore errors except existing best-effort `_ =` sends.
- Ephemeral only — never write to the durable agent roster at runtime.
- Default ad-hoc toolset when `tools` omitted: `[file_read, grep_code, find_symbol]` (read-only).
- Tool name is exactly `agent_dispatch` (the `agent_` prefix makes `buildScopedRegistryStandalone` exclude it from sub-agent scopes — the recursion guard).
- `MaxSubAgentDepth = 5` (matches Claude Code's nesting limit).
- Verify with `make build-bin && make vet`; run agent/gateway tests with the `fts5` tag (`go test -tags fts5 ./internal/agent/ ./internal/gateway/`) because store-backed tests need it.

---

## File Structure

| File | Responsibility | Task |
| --- | --- | --- |
| `internal/agent/subagent.go` | `MaxSubAgentDepth` const + depth-cap rejection in `Spawn` | 1 |
| `internal/agent/subagent_test.go` | depth-cap test | 1 |
| `internal/agent/dispatch_tool.go` | `DispatchTool`: parse inline def, build ephemeral spec, Spawn, map result | 2 |
| `internal/agent/dispatch_tool_test.go` | spec-building defaults + Execute (inline / empty task) | 2 |
| `internal/gateway/gateway.go` | register `agent_dispatch` alongside `workflow` | 3 |
| `internal/gateway/multiagent_wiring_test.go` | assert `agent_dispatch` registers with zero configured agents | 3 |

---

## Task 1: Depth cap in Spawn

**Files:**
- Modify: `internal/agent/subagent.go` (add const + cap check at top of `Spawn`)
- Test: `internal/agent/subagent_test.go`

**Interfaces:**
- Produces: `const MaxSubAgentDepth = 5`; `Spawn` returns an error when `ParentDepth+1 > MaxSubAgentDepth`.

- [ ] **Step 1: Write the failing test**

Add to `internal/agent/subagent_test.go`:

```go
func TestSubAgentManager_Spawn_DepthCapExceeded(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "depth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mgr := NewSubAgentManager(AgentDeps{
		Core: CoreDeps{
			Provider: &mockSubagentProvider{response: "ok"},
			Sessions: session.NewManager(db),
			DB:       db,
			Tools:    tool.NewRegistry(),
			Cfg:      config.AgentConfig{MaxIterations: 1},
			LLMCfg:   config.LLMConfig{Model: "m", MaxTokens: 50},
		},
	}.WithDefaults())

	spec := &AgentSpec{Name: "x", Description: "x"}
	_ = spec.Validate()

	_, err = mgr.Spawn(context.Background(), SpawnRequest{
		Spec: spec, Task: "t", ParentDepth: MaxSubAgentDepth,
	})
	if err == nil {
		t.Fatalf("expected depth-cap error at ParentDepth=%d", MaxSubAgentDepth)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags fts5 ./internal/agent/ -run TestSubAgentManager_Spawn_DepthCapExceeded`
Expected: FAIL — `undefined: MaxSubAgentDepth` (compile error).

- [ ] **Step 3: Add the const and cap check**

In `internal/agent/subagent.go`, add the const near the top of the file (after the imports / above `SpawnRequest`):

```go
// MaxSubAgentDepth bounds how deep sub-agent nesting can go (a sub-agent N
// levels down cannot spawn another). Matches Claude Code's nesting limit.
const MaxSubAgentDepth = 5
```

Then in `Spawn`, add the cap check immediately after `start := time.Now()` (before the `ExecModeBackground` branch):

```go
func (m *SubAgentManager) Spawn(ctx context.Context, req SpawnRequest) (*SubAgentResult, error) {
	start := time.Now()

	if req.ParentDepth+1 > MaxSubAgentDepth {
		return nil, fmt.Errorf("sub-agent depth limit (%d) exceeded", MaxSubAgentDepth)
	}

	if req.Spec.ExecutionMode == ExecModeBackground {
		return m.spawnBackground(ctx, req)
	}
	// ... unchanged ...
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags fts5 ./internal/agent/ -run TestSubAgentManager_Spawn_DepthCapExceeded`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/subagent.go internal/agent/subagent_test.go
git commit -m "feat(agent): enforce sub-agent nesting depth cap (MaxSubAgentDepth=5)"
```

---

## Task 2: `DispatchTool`

**Files:**
- Create: `internal/agent/dispatch_tool.go`
- Test: `internal/agent/dispatch_tool_test.go`

**Interfaces:**
- Consumes: `SubAgentManager.Spawn`, `AgentManager.Get(name) (*AgentSpec, bool)`, `AgentFromContext`, `SubagentContextFromCtx`, `tool.SessionIDFromContext`, `mind.NewCircuitBreaker`, `DefaultMaxIterations`, `MaxSubAgentDepth` (Task 1).
- Produces: `NewDispatchTool(manager *SubAgentManager, agents *AgentManager) *DispatchTool` implementing `tool.Tool` with `Name() == "agent_dispatch"`; `buildDispatchSpec(in dispatchToolInput) *AgentSpec`; `var defaultDispatchTools = []string{"file_read", "grep_code", "find_symbol"}`.

- [ ] **Step 1: Write the failing test**

Create `internal/agent/dispatch_tool_test.go`:

```go
package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/tool"
	"github.com/stretchr/testify/require"
)

func TestBuildDispatchSpec_Defaults(t *testing.T) {
	s := buildDispatchSpec(dispatchToolInput{Task: "t", Prompt: "be a researcher"})
	require.Equal(t, "be a researcher", s.SystemPrompt)
	require.Equal(t, defaultDispatchTools, s.Tools)
	require.NotEmpty(t, s.Description, "description should fall back to a constant")
	require.Equal(t, DefaultMaxIterations, s.MaxIterations)
}

func TestBuildDispatchSpec_ExplicitTools(t *testing.T) {
	s := buildDispatchSpec(dispatchToolInput{Task: "t", Tools: []string{"bash"}, Description: "d"})
	require.Equal(t, []string{"bash"}, s.Tools)
	require.Equal(t, "d", s.Description)
}

func TestDispatchTool_Execute_EmptyTask(t *testing.T) {
	dt := NewDispatchTool(nil, nil)
	out, err := dt.Execute(context.Background(), []byte(`{"prompt":"x"}`))
	require.NoError(t, err)
	require.Equal(t, "task is required", out.Error)
}

func TestDispatchTool_Execute_InlineSpawns(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "dispatch.db"))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mgr := NewSubAgentManager(AgentDeps{
		Core: CoreDeps{
			Provider: &mockSubagentProvider{response: "<result>\n<status>success</status>\n<summary>Found it.</summary>\n</result>"},
			Sessions: session.NewManager(db),
			DB:       db,
			Tools:    tool.NewRegistry(),
			Cfg:      config.AgentConfig{MaxIterations: 2},
			LLMCfg:   config.LLMConfig{Model: "m", MaxTokens: 100},
		},
	}.WithDefaults())

	dt := NewDispatchTool(mgr, nil)
	out, err := dt.Execute(context.Background(), []byte(`{"task":"find the close logic","prompt":"you are a researcher"}`))
	require.NoError(t, err)
	require.Empty(t, out.Error)
	require.NotEmpty(t, out.Output)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags fts5 ./internal/agent/ -run 'TestBuildDispatchSpec|TestDispatchTool'`
Expected: FAIL — `undefined: buildDispatchSpec`, `undefined: NewDispatchTool`, `undefined: dispatchToolInput`, `undefined: defaultDispatchTools`.

- [ ] **Step 3: Write the implementation**

Create `internal/agent/dispatch_tool.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Forest-Isle/daimon/internal/mind"
	"github.com/Forest-Isle/daimon/internal/tool"
)

// dispatchToolInput is the JSON input for agent_dispatch.
type dispatchToolInput struct {
	Description string   `json:"description"`
	Prompt      string   `json:"prompt,omitempty"`
	Task        string   `json:"task"`
	Tools       []string `json:"tools,omitempty"`
	Model       string   `json:"model,omitempty"`
	Agent       string   `json:"agent,omitempty"`
}

// defaultDispatchTools is the read-only tool set an ad-hoc worker gets when the
// caller does not specify `tools`. IronClaw fences tools by posture, so write/
// exec tools must be granted explicitly.
var defaultDispatchTools = []string{"file_read", "grep_code", "find_symbol"}

// DispatchTool spawns an ephemeral sub-agent from an inline definition — no
// pre-registered AgentSpec required. It is the general-purpose delegation tool.
type DispatchTool struct {
	manager *SubAgentManager
	agents  *AgentManager // optional; resolves an `agent` reference to a registered spec
	breaker *mind.CircuitBreaker
}

// NewDispatchTool creates the agent_dispatch tool. agents may be nil (then the
// `agent` reference field is ignored and inline definitions are always used).
func NewDispatchTool(manager *SubAgentManager, agents *AgentManager) *DispatchTool {
	return &DispatchTool{
		manager: manager,
		agents:  agents,
		breaker: mind.NewCircuitBreaker(3, 15*time.Second),
	}
}

func (t *DispatchTool) Name() string { return "agent_dispatch" }

func (t *DispatchTool) Description() string {
	return "Dispatch an ephemeral sub-agent to do a focused task. Provide `task` and a `prompt` describing the worker; optionally `tools` (default: read-only file_read/grep_code/find_symbol), `model`, or `agent` to reference a registered agent. The worker runs isolated and returns its findings."
}

func (t *DispatchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task":        map[string]any{"type": "string", "description": "The concrete task for the worker."},
			"prompt":      map[string]any{"type": "string", "description": "System prompt describing the worker's role."},
			"description": map[string]any{"type": "string", "description": "Short description of when/what this worker is for."},
			"tools":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Allowlist of tool names; empty = read-only default set."},
			"model":       map[string]any{"type": "string", "description": "Optional model override."},
			"agent":       map[string]any{"type": "string", "description": "Optional: name of a registered agent to use instead of an inline definition."},
		},
		"required": []string{"task"},
	}
}

func (t *DispatchTool) RequiresApproval() bool { return true }

// buildDispatchSpec constructs an ephemeral AgentSpec from inline input,
// applying the read-only default toolset and a description fallback (Validate
// requires a non-empty description).
func buildDispatchSpec(in dispatchToolInput) *AgentSpec {
	desc := in.Description
	if desc == "" {
		desc = "Ad-hoc dispatched worker."
	}
	tools := in.Tools
	if len(tools) == 0 {
		tools = defaultDispatchTools
	}
	return &AgentSpec{
		Name:          "dispatch",
		Description:   desc,
		SystemPrompt:  in.Prompt,
		Tools:         tools,
		Model:         in.Model,
		MaxIterations: DefaultMaxIterations,
	}
}

func (t *DispatchTool) Execute(ctx context.Context, input []byte) (tool.Result, error) {
	if !t.breaker.Allow() {
		return tool.Result{Error: "circuit breaker open: agent_dispatch temporarily unavailable"}, nil
	}

	var in dispatchToolInput
	if err := json.Unmarshal(input, &in); err != nil {
		t.breaker.RecordFailure()
		return tool.Result{Error: "invalid input: " + err.Error()}, nil
	}
	if in.Task == "" {
		t.breaker.RecordFailure()
		return tool.Result{Error: "task is required"}, nil
	}

	var spec *AgentSpec
	if in.Agent != "" && t.agents != nil {
		if registered, ok := t.agents.Get(in.Agent); ok {
			spec = registered
		}
	}
	if spec == nil {
		spec = buildDispatchSpec(in)
	}

	var parentID string
	var parentDepth int
	if pa := AgentFromContext(ctx); pa != nil {
		parentID = pa.AgentID()
	}
	if sc := SubagentContextFromCtx(ctx); sc != nil {
		parentDepth = sc.Depth
	}

	result, err := t.manager.Spawn(ctx, SpawnRequest{
		Spec:            spec,
		Task:            in.Task,
		ParentID:        parentID,
		ParentDepth:     parentDepth,
		ParentSessionID: tool.SessionIDFromContext(ctx),
	})
	if err != nil {
		t.breaker.RecordFailure()
		return tool.Result{Error: "dispatch error: " + err.Error()}, nil
	}
	if result.Error != "" {
		t.breaker.RecordFailure()
		return tool.Result{Error: result.Error}, nil
	}
	t.breaker.RecordSuccess()

	out := result.Summary
	if out == "" {
		out = result.Output
	}
	return tool.Result{Output: out}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags fts5 ./internal/agent/ -run 'TestBuildDispatchSpec|TestDispatchTool'`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/agent/dispatch_tool.go internal/agent/dispatch_tool_test.go
git commit -m "feat(agent): add agent_dispatch tool for ad-hoc inline sub-agent dispatch"
```

---

## Task 3: Register `agent_dispatch`

**Files:**
- Modify: `internal/gateway/gateway.go` (register alongside `workflow`)
- Test: `internal/gateway/multiagent_wiring_test.go`

**Interfaces:**
- Consumes: `agent.NewDispatchTool(*agent.SubAgentManager, *agent.AgentManager)` (Task 2).

- [ ] **Step 1: Write the failing test**

Add to `internal/gateway/multiagent_wiring_test.go`:

```go
func TestGatewayRegistersDispatchToolWithoutConfiguredAgents(t *testing.T) {
	cfg := testConfig(t)
	cfg.Agents.Enabled = true
	// No definitions: agent_dispatch must still register — that is its purpose.

	gw, err := New(cfg)
	require.NoError(t, err)
	defer func() { _ = gw.db.Close() }()

	_, err = gw.toolSub.Registry.Get("agent_dispatch")
	require.NoError(t, err, "agent_dispatch should register even with zero configured agents")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags fts5 ./internal/gateway/ -run TestGatewayRegistersDispatchToolWithoutConfiguredAgents`
Expected: FAIL — `Registry.Get("agent_dispatch")` returns a "not found" error.

- [ ] **Step 3: Register the tool**

In `internal/gateway/gateway.go`, inside the existing `if gw.multiAgent.AgentMgr != nil && gw.multiAgent.SubAgentMgr != nil {` block, add the dispatch-tool registration after the `WorkflowTool` registration:

```go
	if gw.multiAgent.AgentMgr != nil && gw.multiAgent.SubAgentMgr != nil {
		gw.toolSub.Registry.Register(agent.NewWorkflowTool(
			gw.multiAgent.AgentMgr,
			gw.multiAgent.SubAgentMgr,
			workflow.NewSQLiteCache(gw.db.DB),
			eventBus,
		))
		gw.toolSub.Registry.Register(agent.NewDispatchTool(
			gw.multiAgent.SubAgentMgr,
			gw.multiAgent.AgentMgr,
		))
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags fts5 ./internal/gateway/ -run TestGatewayRegistersDispatchToolWithoutConfiguredAgents`
Expected: PASS.

- [ ] **Step 5: Full verification**

Run: `make build-bin && make vet && go test -tags fts5 ./internal/agent/ ./internal/gateway/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/gateway.go internal/gateway/multiagent_wiring_test.go
git commit -m "feat(gateway): register agent_dispatch when the multi-agent runtime is up"
```

---

## Self-Review

**Spec coverage:**
- `agent_dispatch` tool with inline def → Task 2.
- Ephemeral spec + read-only default tools + description fallback → Task 2 (`buildDispatchSpec`).
- Optional registered-agent reference → Task 2 (`in.Agent` + `agents.Get`).
- Reuse of `Spawn` / scoping / activity forwarding → Task 2 (Spawn call with `ParentSessionID`/`ParentDepth`).
- Depth cap guardrail → Task 1.
- Always-register regardless of agent count → Task 3.
- `agent_` prefix recursion guard → satisfied by the tool name (no code needed; `buildScopedRegistryStandalone` already drops `agent_*`).
- Approval via interceptor chain → `RequiresApproval() == true` (Task 2).

**Placeholder scan:** none — every step has full code and exact commands.

**Type consistency:** `dispatchToolInput`, `DispatchTool`, `NewDispatchTool`, `buildDispatchSpec`, `defaultDispatchTools` are defined in Task 2 and used consistently in its tests and Task 3's registration. `MaxSubAgentDepth` defined in Task 1, used in Task 1's test (and referenced conceptually by Task 2's depth-1 behavior). `SubAgentResult.Summary`/`.Output`/`.Error`, `AgentManager.Get(name) (*AgentSpec, bool)`, `CircuitBreaker.Allow/RecordSuccess/RecordFailure`, `tool.SessionIDFromContext`, `DefaultMaxIterations` all verified against the codebase.

**Intentional divergence:** none.
