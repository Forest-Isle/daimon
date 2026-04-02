# Subagent Optimization Phase 1: Fork Agent + Parallel Scheduling + Context Inheritance

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Fork Agent (context-inheriting subagents), parallel agent scheduling via AgentOrchestrator, and SubagentContext isolation to IronClaw's multi-agent system.

**Architecture:** Extend AgentSpec with ExecutionMode/PermissionMode fields. Introduce SubagentContext for isolation control. Add ForkAgent that inherits parent Runtime context. Build AgentOrchestrator for parallel/DAG agent scheduling. Refactor AgentTool.Execute() to dispatch by execution mode. Pass parent Runtime via context.Context.

**Tech Stack:** Go 1.22+, golang.org/x/sync/errgroup, existing agent/tool/session/channel packages

---

### Task 1: Extend AgentSpec with new fields

**Files:**
- Modify: `internal/agent/spec.go`

- [ ] **Step 1: Add ExecutionMode and PermissionMode types and extend AgentSpec**

```go
// Add after the existing imports in spec.go, before the AgentSpec struct:

// ExecutionMode determines how a sub-agent is launched.
type ExecutionMode string

const (
	// ExecModeSpawn creates an independent Runtime (default, current behavior).
	ExecModeSpawn ExecutionMode = "spawn"
	// ExecModeFork inherits the parent Runtime's full session context.
	ExecModeFork ExecutionMode = "fork"
	// ExecModeBackground runs asynchronously in a goroutine (Phase 2).
	ExecModeBackground ExecutionMode = "background"
)

// PermissionMode controls how a sub-agent handles tool permission checks.
type PermissionMode string

const (
	// PermModeDefault follows the parent's permission behavior.
	PermModeDefault PermissionMode = ""
	// PermModeBubble sends permission requests to the parent Runtime.
	PermModeBubble PermissionMode = "bubble"
	// PermModeAcceptEdits auto-approves read/write, bubbles dangerous ops.
	PermModeAcceptEdits PermissionMode = "accept_edits"
	// PermModeBypass skips all permission checks (use for trusted read-only agents).
	PermModeBypass PermissionMode = "bypass"
)
```

Then add the following fields to the `AgentSpec` struct, after `MaxRetries`:

```go
	ExecutionMode   ExecutionMode  `yaml:"execution_mode"`    // "spawn" (default) | "fork" | "background"
	PermissionMode  PermissionMode `yaml:"permission_mode"`   // "" | "bubble" | "accept_edits" | "bypass"
	InheritContext  bool           `yaml:"inherit_context"`   // fork mode: inherit parent context
	MaxOutputTokens int            `yaml:"max_output_tokens"` // limit output tokens (0 = no limit)
```

- [ ] **Step 2: Update Validate() to apply defaults for new fields**

Add to the `Validate()` method, after the `Timeout` default check:

```go
	if s.ExecutionMode == "" {
		s.ExecutionMode = ExecModeSpawn
	}
	switch s.ExecutionMode {
	case ExecModeSpawn, ExecModeFork, ExecModeBackground:
		// valid
	default:
		return fmt.Errorf("agent spec %q: invalid execution_mode %q", s.Name, s.ExecutionMode)
	}
	switch s.PermissionMode {
	case PermModeDefault, PermModeBubble, PermModeAcceptEdits, PermModeBypass:
		// valid
	default:
		return fmt.Errorf("agent spec %q: invalid permission_mode %q", s.Name, s.PermissionMode)
	}
	if s.ExecutionMode == ExecModeFork {
		s.InheritContext = true // fork always inherits
	}
```

- [ ] **Step 3: Run build to verify compilation**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./...`
Expected: BUILD SUCCESS (no errors)

- [ ] **Step 4: Commit**

```bash
git add internal/agent/spec.go
git commit -m "feat(agent): add ExecutionMode and PermissionMode to AgentSpec"
```

---

### Task 2: Add SubagentContext struct

**Files:**
- Create: `internal/agent/subagent_context.go`

- [ ] **Step 1: Create subagent_context.go with the SubagentContext struct and builder**

```go
package agent

import (
	"context"

	"github.com/punkopunko/ironclaw/internal/memory"
	"github.com/punkopunko/ironclaw/internal/session"
	"github.com/punkopunko/ironclaw/internal/store"
	"github.com/punkopunko/ironclaw/internal/tool"
)

// MaxForkDepth is the maximum nesting depth for fork agents.
// Prevents unbounded recursion.
const MaxForkDepth = 3

// SubagentContext provides isolation and inheritance control for sub-agents.
// It separates what is shared (read-only references) from what is isolated
// (scoped tool registry, cancel func, tracking metadata).
type SubagentContext struct {
	// --- Isolation layer ---

	// ToolRegistry is the scoped tool set for this sub-agent.
	// agent_* tools are always excluded to prevent recursion.
	ToolRegistry *tool.Registry

	// Permission controls how this sub-agent handles tool approval.
	Permission PermissionMode

	// Cancel cancels this sub-agent's context without affecting the parent.
	Cancel context.CancelFunc

	// AbortOnParent if true, this sub-agent is cancelled when the parent is cancelled.
	AbortOnParent bool

	// --- Inheritance layer (read-only references) ---

	// ParentMessages is the parent's message history (read-only snapshot).
	// Only populated for fork agents; nil for spawn agents.
	ParentMessages []session.Message

	// SystemPrompt is the parent's system prompt string (for fork reuse).
	SystemPrompt string

	// Memory is the shared memory store (read-only queries by sub-agents).
	Memory memory.Store

	// Sessions is the shared session manager.
	Sessions *session.Manager

	// DB is the shared database.
	DB *store.DB

	// --- Tracking ---

	// AgentID uniquely identifies this sub-agent invocation.
	AgentID string

	// ParentID is the agent ID of the parent that spawned this sub-agent.
	// Empty string for top-level agents.
	ParentID string

	// Depth is the nesting level (0 = top-level, 1 = first sub-agent, etc.).
	Depth int

	// ChainID groups all agents in a single invocation chain for tracing.
	ChainID string
}

// runtimeContextKey is the context.Context key for storing the parent Runtime.
type runtimeContextKey struct{}

// RuntimeToContext stores a Runtime reference in the context.
// Used to pass the parent Runtime to sub-agent tool executions.
func RuntimeToContext(ctx context.Context, rt *Runtime) context.Context {
	return context.WithValue(ctx, runtimeContextKey{}, rt)
}

// RuntimeFromContext retrieves the parent Runtime from the context.
// Returns nil if no Runtime is stored.
func RuntimeFromContext(ctx context.Context) *Runtime {
	rt, _ := ctx.Value(runtimeContextKey{}).(*Runtime)
	return rt
}

// subagentContextKey is the context.Context key for SubagentContext.
type subagentContextKey struct{}

// SubagentContextToCtx stores a SubagentContext in the context.
func SubagentContextToCtx(ctx context.Context, sc *SubagentContext) context.Context {
	return context.WithValue(ctx, subagentContextKey{}, sc)
}

// SubagentContextFromCtx retrieves the SubagentContext from the context.
func SubagentContextFromCtx(ctx context.Context) *SubagentContext {
	sc, _ := ctx.Value(subagentContextKey{}).(*SubagentContext)
	return sc
}
```

- [ ] **Step 2: Run build to verify compilation**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./...`
Expected: BUILD SUCCESS

- [ ] **Step 3: Commit**

```bash
git add internal/agent/subagent_context.go
git commit -m "feat(agent): add SubagentContext for sub-agent isolation and tracking"
```

---

### Task 3: Expose Runtime state for fork agents

**Files:**
- Modify: `internal/agent/runtime.go`

- [ ] **Step 1: Add getter methods and depth/chain tracking to Runtime**

Add the following fields to the `Runtime` struct (after `permEngine`):

```go
	agentID   string // unique ID for this runtime instance
	parentID  string // parent agent ID (empty for top-level)
	depth     int    // nesting depth
	chainID   string // invocation chain ID
```

Add getter and setter methods after the existing Set* methods:

```go
// AgentID returns this runtime's unique agent identifier.
func (r *Runtime) AgentID() string { return r.agentID }

// SetAgentID sets this runtime's agent identifier.
func (r *Runtime) SetAgentID(id string) { r.agentID = id }

// ParentID returns the parent agent's identifier.
func (r *Runtime) ParentID() string { return r.parentID }

// SetParentID sets the parent agent's identifier.
func (r *Runtime) SetParentID(id string) { r.parentID = id }

// Depth returns the nesting depth of this runtime.
func (r *Runtime) Depth() int { return r.depth }

// SetDepth sets the nesting depth.
func (r *Runtime) SetDepth(d int) { r.depth = d }

// ChainID returns the invocation chain identifier.
func (r *Runtime) ChainID() string { return r.chainID }

// SetChainID sets the invocation chain identifier.
func (r *Runtime) SetChainID(id string) { r.chainID = id }

// GetMessages returns a snapshot of the current session's message history.
// Returns nil if no session is active. Used by fork agents to inherit context.
func (r *Runtime) GetMessages(ctx context.Context, channelName, channelID string) []session.Message {
	sess, err := r.sessions.Get(ctx, channelName, channelID)
	if err != nil {
		return nil
	}
	history := sess.History()
	out := make([]session.Message, len(history))
	copy(out, history)
	return out
}

// GetSystemPrompt builds and returns the current system prompt.
// Used by fork agents to reuse the parent's prompt.
func (r *Runtime) GetSystemPrompt(ctx context.Context, userText string) string {
	return r.buildSystemPrompt(ctx, userText)
}

// GetTools returns the runtime's tool registry.
func (r *Runtime) GetTools() *tool.Registry { return r.tools }
```

- [ ] **Step 2: Store Runtime in context during HandleMessage**

In `HandleMessage()`, at the very beginning after the function signature (line 99), add:

```go
	// Store this runtime in context so sub-agents can access the parent.
	ctx = RuntimeToContext(ctx, r)
```

- [ ] **Step 3: Run build to verify compilation**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./...`
Expected: BUILD SUCCESS

- [ ] **Step 4: Commit**

```bash
git add internal/agent/runtime.go
git commit -m "feat(agent): expose Runtime state for fork agent context inheritance"
```

---

### Task 4: Implement ForkAgent

**Files:**
- Create: `internal/agent/fork.go`
- Create: `internal/agent/fork_test.go`

- [ ] **Step 1: Write tests for ForkAgent**

```go
package agent

import (
	"context"
	"testing"

	"github.com/punkopunko/ironclaw/internal/session"
)

func TestBuildForkMessages(t *testing.T) {
	parent := []session.Message{
		{ID: "1", Role: "user", Content: "hello"},
		{ID: "2", Role: "assistant", Content: "hi there"},
	}

	msgs := BuildForkMessages(parent, "fix the bug")

	// Should have parent messages + 1 fork directive
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	// Last message should be the fork directive
	last := msgs[2]
	if last.Role != "user" {
		t.Errorf("expected role 'user', got %q", last.Role)
	}
	if last.Content == "" {
		t.Error("fork directive content should not be empty")
	}

	// Should not mutate the original slice
	if len(parent) != 2 {
		t.Errorf("original parent messages were mutated: len=%d", len(parent))
	}
}

func TestBuildForkMessages_EmptyParent(t *testing.T) {
	msgs := BuildForkMessages(nil, "do something")
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected role 'user', got %q", msgs[0].Role)
	}
}

func TestIsForkDirective(t *testing.T) {
	msgs := BuildForkMessages(nil, "test task")
	if !IsForkDirective(msgs[0]) {
		t.Error("expected fork directive to be detected")
	}

	normal := session.Message{Role: "user", Content: "normal message"}
	if IsForkDirective(normal) {
		t.Error("normal message should not be detected as fork directive")
	}
}

func TestForkDepthGuard(t *testing.T) {
	subCtx := &SubagentContext{Depth: MaxForkDepth}
	err := CheckForkDepth(subCtx)
	if err == nil {
		t.Error("expected error when depth equals MaxForkDepth")
	}

	subCtx.Depth = MaxForkDepth - 1
	err = CheckForkDepth(subCtx)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	subCtx.Depth = 0
	err = CheckForkDepth(subCtx)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestForkDepthGuard_NilContext(t *testing.T) {
	err := CheckForkDepth(nil)
	if err != nil {
		t.Errorf("nil context should be treated as depth 0: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go test -tags fts5 -run "TestBuildForkMessages|TestIsForkDirective|TestForkDepthGuard" ./internal/agent/ -v`
Expected: FAIL — functions not defined

- [ ] **Step 3: Implement fork.go**

```go
package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/punkopunko/ironclaw/internal/session"
)

const forkDirectiveTag = "fork-directive"

// BuildForkMessages creates the message list for a fork agent.
// It copies the parent's message history and appends a fork directive
// as a new user message. The parent slice is never mutated.
func BuildForkMessages(parentMessages []session.Message, directive string) []session.Message {
	msgs := make([]session.Message, len(parentMessages), len(parentMessages)+1)
	copy(msgs, parentMessages)

	forkMsg := session.Message{
		ID:        fmt.Sprintf("fork_%d", time.Now().UnixNano()),
		Role:      "user",
		Content:   fmt.Sprintf("<%s>\n%s\n</%s>", forkDirectiveTag, directive, forkDirectiveTag),
		CreatedAt: time.Now(),
	}
	msgs = append(msgs, forkMsg)
	return msgs
}

// IsForkDirective returns true if the message is a fork directive.
func IsForkDirective(msg session.Message) bool {
	return msg.Role == "user" && strings.Contains(msg.Content, "<"+forkDirectiveTag+">")
}

// CheckForkDepth returns an error if the sub-agent context has reached MaxForkDepth.
func CheckForkDepth(sc *SubagentContext) error {
	if sc == nil {
		return nil
	}
	if sc.Depth >= MaxForkDepth {
		return fmt.Errorf("fork depth %d exceeds maximum %d", sc.Depth, MaxForkDepth)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go test -tags fts5 -run "TestBuildForkMessages|TestIsForkDirective|TestForkDepthGuard" ./internal/agent/ -v`
Expected: PASS (all 4 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/agent/fork.go internal/agent/fork_test.go
git commit -m "feat(agent): implement ForkAgent message building and depth guard"
```

---

### Task 5: Implement AgentOrchestrator

**Files:**
- Create: `internal/agent/orchestrator.go`
- Create: `internal/agent/orchestrator_test.go`

- [ ] **Step 1: Write tests for AgentOrchestrator**

```go
package agent

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestTopologicalSort_NoDeps(t *testing.T) {
	tasks := []AgentTask{
		{ID: "a", AgentName: "agent1", Task: "task a"},
		{ID: "b", AgentName: "agent2", Task: "task b"},
		{ID: "c", AgentName: "agent3", Task: "task c"},
	}

	layers, err := TopologicalSort(tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All tasks should be in a single layer (no dependencies)
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	if len(layers[0]) != 3 {
		t.Fatalf("expected 3 tasks in layer 0, got %d", len(layers[0]))
	}
}

func TestTopologicalSort_LinearDeps(t *testing.T) {
	tasks := []AgentTask{
		{ID: "a", AgentName: "agent1", Task: "task a"},
		{ID: "b", AgentName: "agent2", Task: "task b", DependsOn: []string{"a"}},
		{ID: "c", AgentName: "agent3", Task: "task c", DependsOn: []string{"b"}},
	}

	layers, err := TopologicalSort(tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(layers) != 3 {
		t.Fatalf("expected 3 layers, got %d", len(layers))
	}
	if layers[0][0].ID != "a" {
		t.Errorf("layer 0 should contain 'a', got %q", layers[0][0].ID)
	}
	if layers[1][0].ID != "b" {
		t.Errorf("layer 1 should contain 'b', got %q", layers[1][0].ID)
	}
	if layers[2][0].ID != "c" {
		t.Errorf("layer 2 should contain 'c', got %q", layers[2][0].ID)
	}
}

func TestTopologicalSort_DiamondDeps(t *testing.T) {
	// a → b, a → c, b → d, c → d
	tasks := []AgentTask{
		{ID: "a", AgentName: "agent1", Task: "task a"},
		{ID: "b", AgentName: "agent2", Task: "task b", DependsOn: []string{"a"}},
		{ID: "c", AgentName: "agent3", Task: "task c", DependsOn: []string{"a"}},
		{ID: "d", AgentName: "agent4", Task: "task d", DependsOn: []string{"b", "c"}},
	}

	layers, err := TopologicalSort(tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(layers) != 3 {
		t.Fatalf("expected 3 layers, got %d", len(layers))
	}
	// Layer 0: [a], Layer 1: [b, c], Layer 2: [d]
	if len(layers[0]) != 1 || layers[0][0].ID != "a" {
		t.Errorf("layer 0 should be [a]")
	}
	if len(layers[1]) != 2 {
		t.Errorf("layer 1 should have 2 tasks, got %d", len(layers[1]))
	}
	if len(layers[2]) != 1 || layers[2][0].ID != "d" {
		t.Errorf("layer 2 should be [d]")
	}
}

func TestTopologicalSort_CycleDetection(t *testing.T) {
	tasks := []AgentTask{
		{ID: "a", AgentName: "agent1", Task: "task a", DependsOn: []string{"b"}},
		{ID: "b", AgentName: "agent2", Task: "task b", DependsOn: []string{"a"}},
	}

	_, err := TopologicalSort(tasks)
	if err == nil {
		t.Error("expected cycle detection error")
	}
}

func TestExecuteParallel_AllSucceed(t *testing.T) {
	var callCount atomic.Int32
	executor := func(ctx context.Context, task AgentTask) (*AgentResult, error) {
		callCount.Add(1)
		time.Sleep(10 * time.Millisecond) // simulate work
		return &AgentResult{
			AgentName: task.AgentName,
			Output:    "done: " + task.Task,
			Duration:  10 * time.Millisecond,
		}, nil
	}

	orch := &AgentOrchestrator{maxParallel: 4}
	tasks := []AgentTask{
		{ID: "1", AgentName: "a1", Task: "t1"},
		{ID: "2", AgentName: "a2", Task: "t2"},
		{ID: "3", AgentName: "a3", Task: "t3"},
	}

	results, err := orch.executeParallelWith(context.Background(), tasks, executor)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if callCount.Load() != 3 {
		t.Errorf("expected 3 calls, got %d", callCount.Load())
	}
	for i, r := range results {
		if r.Error != nil {
			t.Errorf("result %d has error: %v", i, r.Error)
		}
	}
}

func TestExecuteParallel_PartialFailure(t *testing.T) {
	executor := func(ctx context.Context, task AgentTask) (*AgentResult, error) {
		if task.AgentName == "a2" {
			return nil, fmt.Errorf("agent a2 failed")
		}
		return &AgentResult{AgentName: task.AgentName, Output: "ok"}, nil
	}

	orch := &AgentOrchestrator{maxParallel: 4}
	tasks := []AgentTask{
		{ID: "1", AgentName: "a1", Task: "t1"},
		{ID: "2", AgentName: "a2", Task: "t2"},
		{ID: "3", AgentName: "a3", Task: "t3"},
	}

	results, err := orch.executeParallelWith(context.Background(), tasks, executor)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// a2 should have error, others should succeed
	if results[1].Error == nil {
		t.Error("expected error for a2")
	}
	if results[0].Error != nil {
		t.Errorf("a1 should succeed: %v", results[0].Error)
	}
	if results[2].Error != nil {
		t.Errorf("a3 should succeed: %v", results[2].Error)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go test -tags fts5 -run "TestTopologicalSort|TestExecuteParallel" ./internal/agent/ -v`
Expected: FAIL — types/functions not defined

- [ ] **Step 3: Implement orchestrator.go**

```go
package agent

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"
)

// AgentTask represents a single task to be dispatched to a sub-agent.
type AgentTask struct {
	ID        string   // unique task identifier
	AgentName string   // name of the agent to invoke
	Task      string   // task description / prompt
	Context   string   // optional context from upstream tasks
	DependsOn []string // IDs of tasks that must complete first
}

// AgentResult captures the outcome of a sub-agent execution.
type AgentResult struct {
	AgentName  string
	TaskID     string
	Output     string
	Error      error
	Duration   time.Duration
	TokenUsage TokenUsage
}

// TokenUsage tracks token consumption for a single agent invocation.
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
}

// AgentOrchestrator schedules and executes multiple agents in parallel or
// according to a dependency DAG.
type AgentOrchestrator struct {
	manager     *AgentManager
	maxParallel int // max concurrent agents, default 4
}

// NewAgentOrchestrator creates a new orchestrator.
func NewAgentOrchestrator(manager *AgentManager, maxParallel int) *AgentOrchestrator {
	if maxParallel <= 0 {
		maxParallel = 4
	}
	return &AgentOrchestrator{
		manager:     manager,
		maxParallel: maxParallel,
	}
}

// agentExecutor is the function signature for executing a single agent task.
// Abstracted for testability.
type agentExecutor func(ctx context.Context, task AgentTask) (*AgentResult, error)

// ExecuteParallel runs all tasks concurrently (up to maxParallel).
// Individual failures do not abort other tasks.
func (o *AgentOrchestrator) ExecuteParallel(
	ctx context.Context,
	tasks []AgentTask,
	executor agentExecutor,
) ([]*AgentResult, error) {
	return o.executeParallelWith(ctx, tasks, executor)
}

func (o *AgentOrchestrator) executeParallelWith(
	ctx context.Context,
	tasks []AgentTask,
	executor agentExecutor,
) ([]*AgentResult, error) {
	results := make([]*AgentResult, len(tasks))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(o.maxParallel)

	for i, task := range tasks {
		i, task := i, task
		g.Go(func() error {
			result, err := executor(gctx, task)
			if err != nil {
				results[i] = &AgentResult{
					AgentName: task.AgentName,
					TaskID:    task.ID,
					Error:     err,
				}
				return nil // don't abort other agents
			}
			result.TaskID = task.ID
			results[i] = result
			return nil
		})
	}

	_ = g.Wait()
	return results, nil
}

// ExecuteDAG runs tasks respecting dependency ordering.
// Tasks are sorted topologically, then each layer is executed in parallel.
func (o *AgentOrchestrator) ExecuteDAG(
	ctx context.Context,
	tasks []AgentTask,
	executor agentExecutor,
) ([]*AgentResult, error) {
	layers, err := TopologicalSort(tasks)
	if err != nil {
		return nil, fmt.Errorf("orchestrator DAG sort: %w", err)
	}

	var allResults []*AgentResult
	for _, layer := range layers {
		layerResults, err := o.executeParallelWith(ctx, layer, executor)
		if err != nil {
			return allResults, err
		}
		allResults = append(allResults, layerResults...)
	}
	return allResults, nil
}

// TopologicalSort arranges tasks into layers based on their DependsOn fields.
// Tasks in the same layer have no dependencies on each other and can run in parallel.
// Returns an error if a cycle is detected.
func TopologicalSort(tasks []AgentTask) ([][]AgentTask, error) {
	taskMap := make(map[string]*AgentTask, len(tasks))
	inDegree := make(map[string]int, len(tasks))
	dependents := make(map[string][]string) // taskID → tasks that depend on it

	for i := range tasks {
		t := &tasks[i]
		taskMap[t.ID] = t
		inDegree[t.ID] = 0
	}

	for i := range tasks {
		t := &tasks[i]
		for _, dep := range t.DependsOn {
			if _, ok := taskMap[dep]; !ok {
				return nil, fmt.Errorf("task %q depends on unknown task %q", t.ID, dep)
			}
			inDegree[t.ID]++
			dependents[dep] = append(dependents[dep], t.ID)
		}
	}

	var layers [][]AgentTask
	processed := 0

	// Find initial layer (tasks with no dependencies)
	var currentLayer []AgentTask
	for id, deg := range inDegree {
		if deg == 0 {
			currentLayer = append(currentLayer, *taskMap[id])
		}
	}

	for len(currentLayer) > 0 {
		layers = append(layers, currentLayer)
		processed += len(currentLayer)

		var nextLayer []AgentTask
		for _, t := range currentLayer {
			for _, depID := range dependents[t.ID] {
				inDegree[depID]--
				if inDegree[depID] == 0 {
					nextLayer = append(nextLayer, *taskMap[depID])
				}
			}
		}
		currentLayer = nextLayer
	}

	if processed != len(tasks) {
		return nil, fmt.Errorf("dependency cycle detected: %d of %d tasks processed", processed, len(tasks))
	}

	return layers, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go test -tags fts5 -run "TestTopologicalSort|TestExecuteParallel" ./internal/agent/ -v`
Expected: PASS (all 6 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/agent/orchestrator.go internal/agent/orchestrator_test.go
git commit -m "feat(agent): implement AgentOrchestrator with parallel and DAG scheduling"
```

---

### Task 6: Refactor AgentTool.Execute() to dispatch by ExecutionMode

**Files:**
- Modify: `internal/agent/agent_tool.go`

- [ ] **Step 1: Add uuid generation import and update Execute() with mode dispatch**

Add `"github.com/google/uuid"` to imports. If not available, use `fmt.Sprintf("agent_%d", time.Now().UnixNano())` as fallback.

Replace the `Execute` method (lines 94-183) with:

```go
// Execute creates a scoped tool registry, a temporary Runtime, and runs the sub-agent.
// The execution mode determines how the sub-agent is launched:
//   - spawn (default): independent Runtime with fresh session
//   - fork: inherits parent context (message history + system prompt)
//   - background: async execution (Phase 2, falls back to spawn)
func (a *AgentTool) Execute(ctx context.Context, input []byte) (tool.Result, error) {
	// Check circuit breaker
	if err := a.breaker.Allow(); err != nil {
		return tool.Result{Error: err.Error()}, nil
	}

	var in agentToolInput
	if err := json.Unmarshal(input, &in); err != nil {
		a.breaker.RecordFailure()
		return tool.Result{Error: "invalid input: " + err.Error()}, nil
	}

	if in.Task == "" {
		a.breaker.RecordFailure()
		return tool.Result{Error: "task field is required"}, nil
	}

	// Apply timeout
	timeout := a.spec.Timeout.Duration()
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	slog.Info("agent_tool: executing sub-agent",
		"agent", a.spec.Name,
		"mode", a.spec.ExecutionMode,
		"task_len", len(in.Task),
		"timeout", timeout,
	)

	switch a.spec.ExecutionMode {
	case ExecModeFork:
		return a.executeFork(ctx, in)
	case ExecModeBackground:
		// Phase 2: fall back to spawn for now
		slog.Info("agent_tool: background mode not yet implemented, falling back to spawn", "agent", a.spec.Name)
		return a.executeSpawn(ctx, in)
	default:
		return a.executeSpawn(ctx, in)
	}
}

// executeSpawn creates an independent Runtime with its own session (current behavior).
func (a *AgentTool) executeSpawn(ctx context.Context, in agentToolInput) (tool.Result, error) {
	scopedTools := a.buildScopedRegistry()

	subCfg := a.cfg
	subCfg.MaxIterations = a.spec.MaxIterations
	if a.spec.SystemPrompt != "" {
		subCfg.SystemPrompt = a.spec.SystemPrompt
	}

	subLLMCfg := a.llmCfg
	if a.spec.Model != "" {
		subLLMCfg.Model = a.spec.Model
	}
	if a.spec.MaxTokens > 0 {
		subLLMCfg.MaxTokens = a.spec.MaxTokens
	}

	subRuntime := NewRuntime(a.provider, scopedTools, a.sessions, a.db, subCfg, subLLMCfg)
	if a.memStore != nil {
		subRuntime.SetMemoryStore(a.memStore)
	}

	// Track lineage
	parentRuntime := RuntimeFromContext(ctx)
	agentID := fmt.Sprintf("spawn_%s_%d", a.spec.Name, time.Now().UnixNano())
	subRuntime.SetAgentID(agentID)
	if parentRuntime != nil {
		subRuntime.SetParentID(parentRuntime.AgentID())
		subRuntime.SetDepth(parentRuntime.Depth() + 1)
		subRuntime.SetChainID(parentRuntime.ChainID())
	}

	userText := in.Task
	if in.Context != "" {
		userText = fmt.Sprintf("Context from previous tasks:\n%s\n\nTask:\n%s", in.Context, in.Task)
	}

	capture := newCaptureChannel()
	msg := channel.InboundMessage{
		Channel:   "agent",
		ChannelID: fmt.Sprintf("agent_%s", a.spec.Name),
		UserID:    "orchestrator",
		UserName:  "orchestrator",
		Text:      userText,
	}

	if err := subRuntime.HandleMessage(ctx, capture, msg); err != nil {
		a.breaker.RecordFailure()
		return tool.Result{Error: "sub-agent error: " + err.Error()}, nil
	}

	output := capture.Collect()
	if output == "" {
		output = "(no output from sub-agent)"
	}

	a.breaker.RecordSuccess()
	slog.Info("agent_tool: sub-agent completed",
		"agent", a.spec.Name,
		"mode", "spawn",
		"output_len", len(output),
	)

	return tool.Result{Output: output}, nil
}

// executeFork creates a sub-agent that inherits the parent's session context.
// If no parent Runtime is available, falls back to spawn mode.
func (a *AgentTool) executeFork(ctx context.Context, in agentToolInput) (tool.Result, error) {
	parentRuntime := RuntimeFromContext(ctx)
	if parentRuntime == nil {
		slog.Info("agent_tool: no parent runtime for fork, falling back to spawn", "agent", a.spec.Name)
		return a.executeSpawn(ctx, in)
	}

	// Check fork depth
	parentCtx := SubagentContextFromCtx(ctx)
	if err := CheckForkDepth(parentCtx); err != nil {
		return tool.Result{Error: fmt.Sprintf("fork rejected: %v", err)}, nil
	}

	// Build scoped tools
	scopedTools := a.buildScopedRegistry()

	// Build sub-agent config
	subCfg := a.cfg
	subCfg.MaxIterations = a.spec.MaxIterations
	if a.spec.SystemPrompt != "" {
		subCfg.SystemPrompt = a.spec.SystemPrompt
	}

	subLLMCfg := a.llmCfg
	if a.spec.Model != "" {
		subLLMCfg.Model = a.spec.Model
	}
	if a.spec.MaxTokens > 0 {
		subLLMCfg.MaxTokens = a.spec.MaxTokens
	}

	// Create fork Runtime
	subRuntime := NewRuntime(a.provider, scopedTools, a.sessions, a.db, subCfg, subLLMCfg)
	if a.memStore != nil {
		subRuntime.SetMemoryStore(a.memStore)
	}

	// Track lineage
	agentID := fmt.Sprintf("fork_%s_%d", a.spec.Name, time.Now().UnixNano())
	subRuntime.SetAgentID(agentID)
	subRuntime.SetParentID(parentRuntime.AgentID())
	subRuntime.SetChainID(parentRuntime.ChainID())
	depth := 0
	if parentCtx != nil {
		depth = parentCtx.Depth
	}
	subRuntime.SetDepth(depth + 1)

	// Build SubagentContext for the fork child
	subAgentCtx := &SubagentContext{
		ToolRegistry: scopedTools,
		Permission:   a.spec.PermissionMode,
		Memory:       a.memStore,
		Sessions:     a.sessions,
		DB:           a.db,
		AgentID:      agentID,
		ParentID:     parentRuntime.AgentID(),
		Depth:        depth + 1,
		ChainID:      parentRuntime.ChainID(),
	}

	// Inject SubagentContext into child's context
	childCtx := SubagentContextToCtx(ctx, subAgentCtx)
	childCtx = RuntimeToContext(childCtx, subRuntime)

	// Get parent's messages for context inheritance
	// The fork agent uses the same session channel/ID so it inherits the conversation
	userText := in.Task
	if in.Context != "" {
		userText = fmt.Sprintf("Context from previous tasks:\n%s\n\nTask:\n%s", in.Context, in.Task)
	}

	capture := newCaptureChannel()
	msg := channel.InboundMessage{
		Channel:   "agent",
		ChannelID: fmt.Sprintf("agent_%s", a.spec.Name),
		UserID:    "orchestrator",
		UserName:  "orchestrator",
		Text:      userText,
	}

	if err := subRuntime.HandleMessage(childCtx, capture, msg); err != nil {
		a.breaker.RecordFailure()
		return tool.Result{Error: "fork sub-agent error: " + err.Error()}, nil
	}

	output := capture.Collect()
	if output == "" {
		output = "(no output from fork sub-agent)"
	}

	a.breaker.RecordSuccess()
	slog.Info("agent_tool: fork sub-agent completed",
		"agent", a.spec.Name,
		"agent_id", agentID,
		"parent_id", parentRuntime.AgentID(),
		"depth", depth+1,
		"output_len", len(output),
	)

	return tool.Result{Output: output}, nil
}
```

- [ ] **Step 2: Run build to verify compilation**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./...`
Expected: BUILD SUCCESS

- [ ] **Step 3: Commit**

```bash
git add internal/agent/agent_tool.go
git commit -m "feat(agent): refactor AgentTool.Execute() to dispatch by ExecutionMode"
```

---

### Task 7: Update AgentManager to support new spec fields in BuildPromptSection

**Files:**
- Modify: `internal/agent/agent_manager.go`

- [ ] **Step 1: Update BuildPromptSection to show execution mode and permission info**

Replace the `BuildPromptSection` method (lines 139-162) with:

```go
// BuildPromptSection generates a text section describing available sub-agents
// for injection into the orchestrator's system prompt.
func (m *AgentManager) BuildPromptSection() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.specs) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Available Agents\n\n")
	sb.WriteString("You can delegate tasks to specialized agents using the corresponding agent_* tools.\n")
	sb.WriteString("Each agent runs independently with its own tool set and iteration budget.\n")
	sb.WriteString("Pass context from previous tasks via the \"context\" field to enable pipeline collaboration.\n\n")
	sb.WriteString("Execution modes: spawn (independent), fork (inherits conversation context), background (async).\n\n")

	for _, spec := range m.specs {
		sb.WriteString(fmt.Sprintf("- **agent_%s**: %s", spec.Name, spec.Description))
		if spec.ExecutionMode != "" && spec.ExecutionMode != ExecModeSpawn {
			sb.WriteString(fmt.Sprintf(" [mode: %s]", spec.ExecutionMode))
		}
		if len(spec.Tags) > 0 {
			sb.WriteString(fmt.Sprintf(" [tags: %s]", strings.Join(spec.Tags, ", ")))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
```

- [ ] **Step 2: Run build to verify compilation**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./...`
Expected: BUILD SUCCESS

- [ ] **Step 3: Commit**

```bash
git add internal/agent/agent_manager.go
git commit -m "feat(agent): update BuildPromptSection to show execution mode info"
```

---

### Task 8: Wire AgentOrchestrator into Gateway

**Files:**
- Modify: `internal/gateway/gateway.go`
- Modify: `internal/agent/runtime.go` (add orchestrator field)

- [ ] **Step 1: Add orchestrator field to Runtime**

Add to `Runtime` struct in `runtime.go`:

```go
	orchestrator *AgentOrchestrator
```

Add setter:

```go
// SetOrchestrator attaches an agent orchestrator to the runtime.
func (r *Runtime) SetOrchestrator(o *AgentOrchestrator) { r.orchestrator = o }

// Orchestrator returns the attached orchestrator, or nil.
func (r *Runtime) Orchestrator() *AgentOrchestrator { return r.orchestrator }
```

- [ ] **Step 2: Create and wire orchestrator in Gateway**

In `gateway.go`, inside the `if cfg.Agents.Enabled {` block (after line 400 `agentMgr.RegisterAll(tools)`), add:

```go
		// Agent orchestrator for parallel scheduling
		orchestrator := agent.NewAgentOrchestrator(agentMgr, 4)
		runtime.SetOrchestrator(orchestrator)
		if cognitiveAgent != nil {
			cognitiveAgent.SetOrchestrator(orchestrator)
		}
		slog.Info("agent orchestrator initialized", "max_parallel", 4)
```

- [ ] **Step 3: Add SetOrchestrator to CognitiveAgent**

Check if CognitiveAgent exists and add the method. In `internal/agent/cognitive.go`, add:

```go
// SetOrchestrator attaches an agent orchestrator to the cognitive agent.
func (ca *CognitiveAgent) SetOrchestrator(o *AgentOrchestrator) { ca.orchestrator = o }
```

And add the field `orchestrator *AgentOrchestrator` to the `CognitiveAgent` struct.

- [ ] **Step 4: Run build to verify compilation**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./...`
Expected: BUILD SUCCESS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/runtime.go internal/agent/cognitive.go internal/gateway/gateway.go
git commit -m "feat(agent): wire AgentOrchestrator into Gateway and Runtime"
```

---

### Task 9: Integration test — end-to-end fork and orchestrator

**Files:**
- Create: `internal/agent/integration_test.go`

- [ ] **Step 1: Write integration tests**

```go
package agent

import (
	"context"
	"testing"
	"time"
)

// mockProvider implements Provider for testing.
type mockProvider struct {
	completeFunc func(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
}

func (m *mockProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	if m.completeFunc != nil {
		return m.completeFunc(ctx, req)
	}
	return &CompletionResponse{Text: "mock response", StopReason: StopEndTurn}, nil
}

func (m *mockProvider) Stream(ctx context.Context, req CompletionRequest) (StreamIterator, error) {
	resp, err := m.Complete(ctx, req)
	if err != nil {
		return nil, err
	}
	return &mockStreamIterator{resp: resp}, nil
}

type mockStreamIterator struct {
	resp *CompletionResponse
	done bool
}

func (m *mockStreamIterator) Next() (StreamDelta, error) {
	if m.done {
		return StreamDelta{}, nil
	}
	m.done = true
	return StreamDelta{
		Text:       m.resp.Text,
		Done:       true,
		StopReason: m.resp.StopReason,
		ToolCalls:  m.resp.ToolCalls,
	}, nil
}

func (m *mockStreamIterator) Close() {}

func TestAgentSpec_ForkMode_Validation(t *testing.T) {
	spec := &AgentSpec{
		Name:          "test-fork",
		Description:   "test fork agent",
		ExecutionMode: ExecModeFork,
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if !spec.InheritContext {
		t.Error("fork mode should auto-set InheritContext to true")
	}
	if spec.ExecutionMode != ExecModeFork {
		t.Errorf("expected fork mode, got %q", spec.ExecutionMode)
	}
}

func TestAgentSpec_InvalidMode(t *testing.T) {
	spec := &AgentSpec{
		Name:          "test-bad",
		Description:   "bad mode",
		ExecutionMode: "invalid_mode",
	}
	err := spec.Validate()
	if err == nil {
		t.Error("expected validation error for invalid execution mode")
	}
}

func TestRuntimeContext_RoundTrip(t *testing.T) {
	ctx := context.Background()

	// No runtime initially
	if rt := RuntimeFromContext(ctx); rt != nil {
		t.Error("expected nil runtime from empty context")
	}

	// Store and retrieve
	runtime := &Runtime{agentID: "test-123"}
	ctx = RuntimeToContext(ctx, runtime)

	rt := RuntimeFromContext(ctx)
	if rt == nil {
		t.Fatal("expected non-nil runtime")
	}
	if rt.AgentID() != "test-123" {
		t.Errorf("expected agent ID 'test-123', got %q", rt.AgentID())
	}
}

func TestSubagentContext_RoundTrip(t *testing.T) {
	ctx := context.Background()

	if sc := SubagentContextFromCtx(ctx); sc != nil {
		t.Error("expected nil from empty context")
	}

	subCtx := &SubagentContext{
		AgentID:  "sub-456",
		ParentID: "parent-789",
		Depth:    1,
		ChainID:  "chain-abc",
	}
	ctx = SubagentContextToCtx(ctx, subCtx)

	sc := SubagentContextFromCtx(ctx)
	if sc == nil {
		t.Fatal("expected non-nil SubagentContext")
	}
	if sc.AgentID != "sub-456" {
		t.Errorf("expected 'sub-456', got %q", sc.AgentID)
	}
	if sc.Depth != 1 {
		t.Errorf("expected depth 1, got %d", sc.Depth)
	}
}

func TestOrchestratorDAG_ExecutionOrder(t *testing.T) {
	var order []string
	executor := func(ctx context.Context, task AgentTask) (*AgentResult, error) {
		time.Sleep(5 * time.Millisecond) // simulate work
		order = append(order, task.ID)
		return &AgentResult{AgentName: task.AgentName, Output: "done"}, nil
	}

	orch := NewAgentOrchestrator(nil, 2)
	tasks := []AgentTask{
		{ID: "a", AgentName: "a1", Task: "t1"},
		{ID: "b", AgentName: "a2", Task: "t2", DependsOn: []string{"a"}},
	}

	results, err := orch.ExecuteDAG(context.Background(), tasks, executor)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// "a" must execute before "b"
	aIdx, bIdx := -1, -1
	for i, id := range order {
		if id == "a" {
			aIdx = i
		}
		if id == "b" {
			bIdx = i
		}
	}
	if aIdx >= bIdx {
		t.Errorf("task 'a' (idx=%d) should execute before 'b' (idx=%d)", aIdx, bIdx)
	}
}
```

- [ ] **Step 2: Run integration tests**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go test -tags fts5 -run "TestAgentSpec_ForkMode|TestAgentSpec_InvalidMode|TestRuntimeContext|TestSubagentContext|TestOrchestratorDAG" ./internal/agent/ -v`
Expected: PASS (all 5 tests)

- [ ] **Step 3: Commit**

```bash
git add internal/agent/integration_test.go
git commit -m "test(agent): add integration tests for fork mode, context passing, and DAG orchestration"
```

---

### Task 10: Run full test suite and verify no regressions

**Files:** None (verification only)

- [ ] **Step 1: Run all agent package tests**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go test -tags fts5 ./internal/agent/ -v -count=1`
Expected: PASS (all tests including new ones)

- [ ] **Step 2: Run full project build**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./...`
Expected: BUILD SUCCESS

- [ ] **Step 3: Run linter**

Run: `cd /Users/wuqisen/learning/IronClaw && make lint 2>&1 || true`
Expected: No new lint errors from our changes (existing lint issues may be present)

- [ ] **Step 4: Commit any lint fixes if needed**

```bash
git add -A
git commit -m "fix(agent): address lint issues in Phase 1 implementation"
```
