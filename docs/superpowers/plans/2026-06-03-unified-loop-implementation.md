# Unified Loop + Planning-as-Tool: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace dual-mode agent architecture (Simple + 5-phase Cognitive) with a single UnifiedLoop where LLM autonomously decides task decomposition via the `plan_task` tool.

**Architecture:** Extract DAG executor from `act.go` into `internal/dag/` (zero-dependency package). Build `plan_task` tool on top of it. Create `UnifiedLoop` with parallel tool dispatch + merged context assembly. Delete 14 files (~3,500 LOC), add 5 files (~1,100 LOC). Net: ~35% reduction in agent orchestration code.

**Tech Stack:** Go 1.22+, standard library + `github.com/Forest-Isle/IronClaw` internal packages

---

### Task 1: Create DAG Executor Package

**Files:**
- Create: `internal/dag/executor.go`
- Create: `internal/dag/executor_test.go`

- [ ] **Step 1: Write the DAG package skeleton**

```go
// internal/dag/executor.go
package dag

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// Status is the execution status of a single DAG task.
type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
	StatusSkipped Status = "skipped"
)

// Task is a single node in the execution DAG.
type Task struct {
	ID          string
	Description string
	ToolName    string
	ToolInput   string
	DependsOn   []string
}

// Result is the outcome of executing a single task.
type Result struct {
	TaskID     string
	Output     string
	Error      string
	DurationMs int64
	Status     Status
}

// ExecuteFunc is called by the DAG executor for each task.
// The executor handles parallelism, dependency ordering, and failure propagation.
type ExecuteFunc func(ctx context.Context, t Task) Result

// Execute runs all tasks respecting their dependency DAG.
// Independent tasks run concurrently (up to maxParallel). Tasks that depend
// on failed tasks are skipped. Returns results in non-deterministic order.
// If maxParallel <= 0, it defaults to len(tasks).
func Execute(ctx context.Context, tasks []Task, exec ExecuteFunc, maxParallel int) []Result {
	if len(tasks) == 0 {
		return nil
	}
	if maxParallel <= 0 {
		maxParallel = len(tasks)
	}

	// Build lookup index
	byID := make(map[string]*Task, len(tasks))
	for i := range tasks {
		byID[tasks[i].ID] = &tasks[i]
	}

	// Track per-task status using atomic-friendly slices
	type entry struct {
		task   *Task
		status Status
		result Result
	}
	entries := make([]entry, len(tasks))
	idToIdx := make(map[string]int, len(tasks))
	for i := range tasks {
		entries[i] = entry{task: &tasks[i], status: StatusPending}
		idToIdx[tasks[i].ID] = i
	}

	var mu sync.Mutex
	var doneCount int32
	total := len(tasks)

	readyCh := make(chan int, total) // feeds indices of ready tasks
	sem := make(chan struct{}, maxParallel)

	// Seed: tasks with no dependencies
	for i, t := range tasks {
		if len(t.DependsOn) == 0 {
			readyCh <- i
		}
	}

	var wg sync.WaitGroup
	workerCount := maxParallel
	if workerCount > total {
		workerCount = total
	}
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case idx, ok := <-readyCh:
					if !ok {
						return
					}
					select {
					case sem <- struct{}{}:
					case <-ctx.Done():
						return
					}

					mu.Lock()
					entries[idx].status = StatusRunning
					e := &entries[idx]
					mu.Unlock()

					result := exec(ctx, *e.task)

					mu.Lock()
					e.result = result
					e.status = result.Status
					n := int(atomic.AddInt32(&doneCount, 1))
					mu.Unlock()
					_ = n

					<-sem

					// Unblock tasks whose dependencies are now satisfied
					mu.Lock()
					for j, ent := range entries {
						if ent.status != StatusPending {
							continue
						}
						if allDepsSatisfied(ent.task.DependsOn, entries, idToIdx) {
							entries[j].status = StatusPending // will be set to Running by worker
							select {
							case readyCh <- j:
							default:
							}
						}
					}
					mu.Unlock()
				}
			}
		}()
	}

	wg.Wait()
	close(readyCh)

	// Collect results
	results := make([]Result, 0, total)
	for _, e := range entries {
		if e.result.TaskID != "" {
			results = append(results, e.result)
		}
	}
	return results
}

// allDepsSatisfied returns true when every dependency of t is done, failed, or skipped.
func allDepsSatisfied(depIDs []string, entries []entry, idToIdx map[string]int) bool {
	for _, depID := range depIDs {
		idx, ok := idToIdx[depID]
		if !ok {
			continue // unknown dep: treat as satisfied
		}
		switch entries[idx].status {
		case StatusDone, StatusFailed, StatusSkipped:
			continue
		default:
			return false
		}
	}
	return true
}

// emoji returns a single-character status indicator.
func statusEmoji(s Status) string {
	switch s {
	case StatusDone:
		return "✅"
	case StatusFailed:
		return "❌"
	case StatusSkipped:
		return "⏭️"
	default:
		return "⏳"
	}
}

// FormatResults returns a human-readable summary of execution results.
func FormatResults(results []Result) string {
	if len(results) == 0 {
		return "No tasks executed."
	}
	var out string
	successes, failures, skipped := 0, 0, 0
	for _, r := range results {
		switch r.Status {
		case StatusDone:
			successes++
		case StatusFailed:
			failures++
		case StatusSkipped:
			skipped++
		}
		out += fmt.Sprintf("%s [%s] %s\n", statusEmoji(r.Status), r.TaskID, truncate(r.Output+truncate(r.Error, ""), 200))
	}
	out += fmt.Sprintf("\n---\n%d succeeded, %d failed, %d skipped", successes, failures, skipped)
	return out
}

func truncate(s string, default_ string) string {
	if s == "" {
		return default_
	}
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
```

- [ ] **Step 2: Write DAG executor tests**

```go
// internal/dag/executor_test.go
package dag

import (
	"context"
	"testing"
	"time"
)

func TestExecuteEmpty(t *testing.T) {
	results := Execute(context.Background(), nil, nil, 3)
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestExecuteSingle(t *testing.T) {
	tasks := []Task{{ID: "1", Description: "test", ToolName: "echo", ToolInput: "hello"}}
	exec := func(ctx context.Context, t Task) Result {
		return Result{TaskID: t.ID, Output: "hello", Status: StatusDone}
	}
	results := Execute(context.Background(), tasks, exec, 3)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != StatusDone {
		t.Fatalf("expected done, got %s", results[0].Status)
	}
}

func TestExecuteParallelIndependence(t *testing.T) {
	tasks := []Task{
		{ID: "a", Description: "task a"},
		{ID: "b", Description: "task b"},
		{ID: "c", Description: "task c"},
	}
	order := make(chan string, 3)
	exec := func(ctx context.Context, t Task) Result {
		time.Sleep(10 * time.Millisecond)
		order <- t.ID
		return Result{TaskID: t.ID, Output: t.ID, Status: StatusDone}
	}
	results := Execute(context.Background(), tasks, exec, 3)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	close(order)
	ids := make(map[string]bool)
	for id := range order {
		ids[id] = true
	}
	if len(ids) != 3 {
		t.Fatal("not all tasks executed")
	}
}

func TestExecuteDependencyChain(t *testing.T) {
	tasks := []Task{
		{ID: "1", Description: "first"},
		{ID: "2", Description: "second", DependsOn: []string{"1"}},
		{ID: "3", Description: "third", DependsOn: []string{"2"}},
	}
	var mu sync.Mutex
	var order []string
	exec := func(ctx context.Context, t Task) Result {
		mu.Lock()
		order = append(order, t.ID)
		mu.Unlock()
		return Result{TaskID: t.ID, Output: t.ID, Status: StatusDone}
	}
	results := Execute(context.Background(), tasks, exec, 1) // maxParallel=1 forces serial
	if len(results) != 3 {
		t.Fatalf("expected 3, got %d", len(results))
	}
	if order[0] != "1" || order[1] != "2" || order[2] != "3" {
		t.Fatalf("expected [1 2 3], got %v", order)
	}
}

func TestExecuteFailurePropagation(t *testing.T) {
	tasks := []Task{
		{ID: "ok", Description: "succeeds"},
		{ID: "fail", Description: "fails"},
		{ID: "skip", Description: "skipped", DependsOn: []string{"fail"}},
	}
	exec := func(ctx context.Context, t Task) Result {
		if t.ID == "fail" {
			return Result{TaskID: t.ID, Error: "boom", Status: StatusFailed}
		}
		return Result{TaskID: t.ID, Output: t.ID, Status: StatusDone}
	}
	results := Execute(context.Background(), tasks, exec, 3)
	if len(results) != 3 {
		t.Fatalf("expected 3, got %d", len(results))
	}
	for _, r := range results {
		switch r.TaskID {
		case "ok":
			if r.Status != StatusDone {
				t.Errorf("ok should be done, got %s", r.Status)
			}
		case "fail":
			if r.Status != StatusFailed {
				t.Errorf("fail should be failed, got %s", r.Status)
			}
		case "skip":
			if r.Status != StatusSkipped {
				t.Errorf("skip should be skipped, got %s", r.Status)
			}
		}
	}
}

func TestFormatResults(t *testing.T) {
	results := []Result{
		{TaskID: "1", Output: "ok", Status: StatusDone},
		{TaskID: "2", Error: "boom", Status: StatusFailed},
	}
	out := FormatResults(results)
	if out == "" {
		t.Fatal("expected non-empty output")
	}
}
```

- [ ] **Step 3: Run tests to verify they pass**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go test -tags "fts5" ./internal/dag/ -v -count=1
```

Expected: PASS for all 6 tests (including the FormatResults test).

```
import addition: add "sync" to imports in executor_test.go for TestExecuteDependencyChain
```

- [ ] **Step 4: Commit**

```bash
git add internal/dag/executor.go internal/dag/executor_test.go
git commit -m "feat: extract DAG executor from act.go into internal/dag/
- Zero-dependency package: worker pool + channel + semaphore pattern
- Topological readiness: independent tasks run concurrently
- Failure propagation: dependent tasks skipped when upstream fails
- 6 tests: empty, single, parallel, chain, failure, format"
```

---

### Task 2: Create plan_task Tool

**Files:**
- Create: `internal/tool/plan_task.go`
- Create: `internal/tool/plan_task_test.go`
- Reference: `internal/agent/act.go:222-291` (executeSubTask and executeSubTaskViaChain)

- [ ] **Step 1: Write plan_task tool implementation**

```go
// internal/tool/plan_task.go
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/dag"
	"github.com/Forest-Isle/IronClaw/internal/hook"
)

// PlanTaskTool is a tool that LLMs can call to decompose and execute
// complex multi-step tasks with DAG-based parallel scheduling.
type PlanTaskTool struct {
	registry       *Registry
	maxParallel    int
	singleExecutor SingleToolExecutor
	hookMgr        *hook.Manager
	permEngine     *PermissionEngine
	interceptor    *InterceptorChain
}

// SingleToolExecutor executes a single tool by name with raw JSON input.
// Injected at construction time to avoid circular dependencies (the agent
// package owns the full executeToolCall pipeline).
type SingleToolExecutor interface {
	Execute(ctx context.Context, toolName, input string) (output string, err error)
}

// NewPlanTaskTool creates a PlanTaskTool.
func NewPlanTaskTool(
	registry *Registry,
	maxParallel int,
	executor SingleToolExecutor,
	hookMgr *hook.Manager,
	permEngine *PermissionEngine,
	interceptor *InterceptorChain,
) *PlanTaskTool {
	if maxParallel <= 0 {
		maxParallel = 5
	}
	return &PlanTaskTool{
		registry:       registry,
		maxParallel:    maxParallel,
		singleExecutor: executor,
		hookMgr:        hookMgr,
		permEngine:     permEngine,
		interceptor:    interceptor,
	}
}

// planTaskInput is the JSON structure the LLM provides.
type planTaskInput struct {
	SubTasks []planSubTask `json:"subtasks"`
}

type planSubTask struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	ToolName    string   `json:"tool_name"`
	ToolInput   string   `json:"tool_input"`
	DependsOn   []string `json:"depends_on"`
	Confidence  float64  `json:"confidence"`
}

func (p *PlanTaskTool) Name() string        { return "plan_task" }
func (p *PlanTaskTool) IsReadOnly() bool     { return false }
func (p *PlanTaskTool) RequiresApproval() bool { return false }

func (p *PlanTaskTool) Description() string {
	return "Decompose a complex request into independently executable subtasks with dependency tracking. " +
		"Subtasks with no mutual dependencies run in parallel. " +
		"Use this when a request requires multiple distinct operations across different tools. " +
		"For single-tool requests, call the tool directly instead. " +
		"Each subtask needs: id, description, tool_name, tool_input, depends_on (optional array of task IDs), confidence (0.0-1.0)."
}

func (p *PlanTaskTool) InputSchema() string {
	return `{
  "type": "object",
  "properties": {
    "subtasks": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "id": {"type": "string", "description": "Unique identifier for this subtask"},
          "description": {"type": "string", "description": "What this task does, in imperative form"},
          "tool_name": {"type": "string", "description": "The tool to invoke (bash, read, write, edit, etc.)"},
          "tool_input": {"type": "string", "description": "JSON input for the tool"},
          "depends_on": {"type": "array", "items": {"type": "string"}, "description": "IDs of tasks that must complete first"},
          "confidence": {"type": "number", "description": "How confident you are this task will succeed (0.0-1.0)"}
        },
        "required": ["id", "description", "tool_name", "tool_input"]
      }
    }
  },
  "required": ["subtasks"]
}`
}

func (p *PlanTaskTool) Execute(ctx context.Context, input []byte) (*Result, error) {
	var plan planTaskInput
	if err := json.Unmarshal(input, &plan); err != nil {
		return &Result{Error: fmt.Sprintf("plan_task: invalid JSON input: %v", err)}, nil
	}

	if len(plan.SubTasks) == 0 {
		return &Result{Output: "No subtasks provided."}, nil
	}

	// Convert to DAG tasks
	dagTasks := make([]dag.Task, len(plan.SubTasks))
	for i, st := range plan.SubTasks {
		dagTasks[i] = dag.Task{
			ID:          st.ID,
			Description: st.Description,
			ToolName:    st.ToolName,
			ToolInput:   st.ToolInput,
			DependsOn:   st.DependsOn,
		}
	}

	// Build execute function wrapping single-tool execution
	execFn := func(ctx context.Context, t dag.Task) dag.Result {
		start := time.Now()

		// If no tool name, this is a direct-reply subtask (shouldn't normally happen)
		if t.ToolName == "" {
			return dag.Result{TaskID: t.ID, Output: t.Description, Status: dag.StatusDone}
		}

		output, err := p.singleExecutor.Execute(ctx, t.ToolName, t.ToolInput)
		dur := time.Since(start).Milliseconds()

		if err != nil {
			return dag.Result{TaskID: t.ID, Error: err.Error(), DurationMs: dur, Status: dag.StatusFailed}
		}
		return dag.Result{TaskID: t.ID, Output: output, DurationMs: dur, Status: dag.StatusDone}
	}

	results := dag.Execute(ctx, dagTasks, execFn, p.maxParallel)
	formatted := dag.FormatResults(results)

	return &Result{Output: formatted}, nil
}
```

- [ ] **Step 2: Register SingleToolExecutor in agent package**

```go
// Add to internal/agent/agent.go (or a new bridge file)

// toolExecutor adapts Agent.executeToolCall for the plan_task tool.
type toolExecutor struct {
	agent *Agent
}

func (e *toolExecutor) Execute(ctx context.Context, toolName, input string) (string, error) {
	// We need a minimal interceptor call. Since we don't have a channel/session here,
	// we use the interceptor directly.
	call := &tool.ToolCall{ToolName: toolName, Input: input}
	
	t, err := e.agent.deps.Core.Tools.Get(call.ToolName)
	if err != nil {
		return "", fmt.Errorf("tool not found: %s", call.ToolName)
	}
	
	result, err := e.agent.deps.Security.Interceptor.Execute(ctx, call, func(ctx context.Context, call *tool.ToolCall) (*tool.ToolResult, error) {
		r, execErr := t.Execute(ctx, []byte(call.Input))
		if execErr != nil {
			return &tool.ToolResult{Error: execErr.Error()}, nil
		}
		return &tool.ToolResult{Output: r.Output}, nil
	})
	if err != nil {
		return "", err
	}
	if result.Error != "" {
		return "", fmt.Errorf(result.Error)
	}
	return result.Output, nil
}
```

Place this in a new file `internal/agent/tool_bridge.go` (~30 lines).

- [ ] **Step 3: Write plan_task test**

```go
// internal/tool/plan_task_test.go
package tool

import (
	"context"
	"encoding/json"
	"testing"
)

type mockSingleExecutor struct {
	outputs map[string]string
	errors  map[string]string
}

func (m *mockSingleExecutor) Execute(ctx context.Context, toolName, input string) (string, error) {
	if errMsg, ok := m.errors[toolName]; ok {
		return "", fmt.Errorf(errMsg)
	}
	return m.outputs[toolName], nil
}

func TestPlanTaskSingleSubtask(t *testing.T) {
	exec := &mockSingleExecutor{outputs: map[string]string{"read": "file content"}}
	pt := NewPlanTaskTool(nil, 3, exec, nil, nil, nil)
	
	input := `{"subtasks":[{"id":"1","description":"read file","tool_name":"read","tool_input":"{\"path\":\"/tmp/test\"}"}]}`
	result, err := pt.Execute(context.Background(), []byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
	if result.Output == "" {
		t.Fatal("expected output")
	}
}

func TestPlanTaskParallel(t *testing.T) {
	exec := &mockSingleExecutor{outputs: map[string]string{"a": "A", "b": "B", "c": "C"}}
	pt := NewPlanTaskTool(nil, 3, exec, nil, nil, nil)
	
	subtasks := []planSubTask{
		{ID: "a", Description: "task A", ToolName: "a", ToolInput: "{}"},
		{ID: "b", Description: "task B", ToolName: "b", ToolInput: "{}"},
		{ID: "c", Description: "task C", ToolName: "c", ToolInput: "{}"},
	}
	input, _ := json.Marshal(planTaskInput{SubTasks: subtasks})
	result, err := pt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
}

func TestPlanTaskFailure(t *testing.T) {
	exec := &mockSingleExecutor{
		outputs: map[string]string{"ok": "ok"},
		errors:  map[string]string{"fail": "intentional failure"},
	}
	pt := NewPlanTaskTool(nil, 2, exec, nil, nil, nil)
	
	subtasks := []planSubTask{
		{ID: "fail", Description: "failing task", ToolName: "fail", ToolInput: "{}"},
		{ID: "ok", Description: "ok task", ToolName: "ok", ToolInput: "{}"},
	}
	input, _ := json.Marshal(planTaskInput{SubTasks: subtasks})
	result, err := pt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have both ok and fail in output
	if result.Output == "" {
		t.Fatal("expected output with failure info")
	}
}

func TestPlanTaskInvalidJSON(t *testing.T) {
	pt := NewPlanTaskTool(nil, 3, nil, nil, nil, nil)
	result, err := pt.Execute(context.Background(), []byte("not json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestPlanTaskEmptySubtasks(t *testing.T) {
	pt := NewPlanTaskTool(nil, 3, nil, nil, nil, nil)
	result, err := pt.Execute(context.Background(), []byte(`{"subtasks":[]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
}
```

Add `"fmt"` to imports in the test file for TestPlanTaskFailure.

- [ ] **Step 4: Run tests**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go test -tags "fts5" ./internal/tool/ -run TestPlanTask -v -count=1
```

Expected: 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tool/plan_task.go internal/tool/plan_task_test.go internal/agent/tool_bridge.go
git commit -m "feat: add plan_task tool with DAG-based parallel execution
- plan_task wraps dag.Executor for LLM-driven task decomposition
- SingleToolExecutor interface bridges agent executeToolCall into tool layer
- 5 tests: single, parallel, failure, invalid JSON, empty subtasks"
```

---

### Task 3: Create UnifiedLoop

**Files:**
- Create: `internal/agent/unified_loop.go`
- Modify: `internal/agent/agent.go` (add dispatchToolsParallel, assembleContext)

- [ ] **Step 1: Add parallel dispatch to Agent**

Add this method to `internal/agent/agent.go` after `executeToolCall` (line 333):

```go
// dispatchToolsParallel executes multiple independent tool calls concurrently.
// Each call goes through the full executeToolCall pipeline (interceptor + approval + hooks).
func (a *Agent) dispatchToolsParallel(ctx context.Context, ch channel.Channel, sess *session.Session, target channel.MessageTarget, calls []ToolUseBlock, budgetWarning string) {
	if len(calls) == 0 {
		return
	}
	if len(calls) == 1 {
		a.executeToolCall(ctx, ch, sess, target, calls[0], budgetWarning)
		return
	}

	var wg sync.WaitGroup
	for i := range calls {
		wg.Add(1)
		go func(tc ToolUseBlock) {
			defer wg.Done()
			a.executeToolCall(ctx, ch, sess, target, tc, budgetWarning)
		}(calls[i])
	}
	wg.Wait()
}
```

Add `"sync"` to imports if not already present.

- [ ] **Step 2: Write UnifiedLoop**

```go
// internal/agent/unified_loop.go
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	ierrors "github.com/Forest-Isle/IronClaw/internal/errors"
	"github.com/Forest-Isle/IronClaw/internal/session"
)

// UnifiedLoop implements a single agent execution strategy that handles both
// simple single-tool requests and complex multi-step tasks through the same
// LLM → tools → repeat loop. The LLM autonomously decides whether to call
// tools directly or use plan_task for task decomposition.
type UnifiedLoop struct{}

// Execute runs the unified agent loop for a single inbound message.
func (UnifiedLoop) Execute(ctx context.Context, a *Agent, ch channel.Channel, msg channel.InboundMessage, sess *session.Session) error {
	target := channel.MessageTarget{Channel: msg.Channel, ChannelID: msg.ChannelID}
	systemPrompt := a.buildSystemPrompt(ctx, msg.Text)
	maxIter := a.deps.Core.Cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 20
	}

	for iteration := 0; iteration < maxIter; iteration++ {
		slog.Info("unified loop iteration", "iteration", iteration, "session", sess.ID)

		// Reset speculative executor
		if a.deps.MultiAgent.Speculative != nil {
			a.deps.MultiAgent.Speculative.Reset()
		}

		// Budget pressure signal
		budgetWarning := computeBudgetPressure(iteration, maxIter, sess, systemPrompt, a.deps.Memory.ContextMgr)

		// Push metrics
		util := a.deps.Memory.ContextMgr.Utilization(sess, systemPrompt)
		inTok, outTok := int64(0), int64(0)
		cacheCreate, cacheRead := int64(0), int64(0)
		switch p := a.deps.Core.Provider.(type) {
		case *ClaudeProvider:
			cacheCreate, cacheRead = p.GetCacheStats()
			inTok, outTok = p.GetTokenStats()
		case *OpenAIProvider:
			cacheCreate, cacheRead = p.GetCacheStats()
			inTok, outTok = p.GetTokenStats()
		}
		a.eventBus.Publish(MetricsTick{
			SessionID: sess.ID, Iteration: iteration, MaxIter: maxIter,
			Utilization: util, InputTokens: inTok, OutputTokens: outTok,
			CacheCreate: cacheCreate, CacheRead: cacheRead,
			Model: a.deps.Core.LLMCfg.Model, Provider: a.deps.Core.LLMCfg.Provider,
		})

		// Create streaming message
		updater, streamErr := ch.SendStreaming(ctx, target)
		if streamErr != nil {
			return unifiedNonStreaming(ctx, a, ch, sess, target, systemPrompt, maxIter)
		}

		req := CompletionRequest{
			Model:     a.deps.Core.LLMCfg.Model,
			System:    systemPrompt,
			Messages:  BuildMessages(sess),
			Tools:     a.buildToolDefs(),
			MaxTokens: a.deps.Core.LLMCfg.MaxTokens,
		}

		stream, streamErr := a.deps.Core.Provider.Stream(ctx, req)
		if streamErr != nil && isContextLengthError(streamErr) {
			_ = updater.Finish("")
			if compErr := a.deps.Memory.ContextMgr.ReactiveCompress(ctx, sess, systemPrompt); compErr != nil {
				slog.Warn("reactive compress failed", "err", compErr)
			} else {
				a.eventBus.Publish(ContextCompressed{SessionID: sess.ID, Reason: "413_retry", LayersRun: 3})
				req.Messages = BuildMessages(sess)
				stream, streamErr = a.deps.Core.Provider.Stream(ctx, req)
			}
		}
		if streamErr != nil {
			_ = updater.Finish("Error: " + streamErr.Error())
			return fmt.Errorf("llm stream: %w", streamErr)
		}

		var fullText string
		var toolCalls []ToolUseBlock
		var stopReason StopReason

		for {
			delta, deltaErr := stream.Next()
			if deltaErr != nil {
				stream.Close()
				_ = updater.Finish("Error: " + deltaErr.Error())
				return fmt.Errorf("stream next: %w", deltaErr)
			}

			if delta.Text != "" {
				fullText += delta.Text
				_ = updater.Update(fullText)
			}
			if delta.ToolCall != nil {
				toolCalls = append(toolCalls, *delta.ToolCall)
			}
			if delta.Done && len(delta.ToolCalls) > 0 {
				toolCalls = delta.ToolCalls
			}

			// Speculative execution
			if a.deps.MultiAgent.Speculative != nil {
				if ptbSrc, ok := stream.(PendingToolBlockSource); ok {
					for _, ptb := range ptbSrc.PendingToolBlocks() {
						a.deps.MultiAgent.Speculative.TryLaunch(ctx, ptb.ToolUseID, ptb.ToolName, ptb.Input)
					}
				}
			}

			if delta.Done {
				stopReason = delta.StopReason
				break
			}
		}
		stream.Close()

		// Fallback: tool_use without tool calls -> re-request non-streaming
		if stopReason == StopToolUse && len(toolCalls) == 0 {
			resp, completeErr := a.deps.Core.Provider.Complete(ctx, req)
			if completeErr != nil {
				_ = updater.Finish("Error: " + completeErr.Error())
				return completeErr
			}
			fullText = resp.Text
			toolCalls = resp.ToolCalls
		}

		// Save assistant message
		if fullText != "" {
			sess.AddMessage(session.Message{
				ID: fmt.Sprintf("msg_%d", time.Now().UnixNano()),
				Role: "assistant", Content: fullText, CreatedAt: time.Now(),
			})
		}

		// Save tool_use messages
		for _, tc := range toolCalls {
			sess.AddMessage(session.Message{
				ID: tc.ID, Role: "tool_use", ToolName: tc.Name,
				ToolInput: tc.Input, CreatedAt: time.Now(),
			})
		}

		// If no tool calls, done
		if len(toolCalls) == 0 {
			_ = updater.Finish(fullText)
			return nil
		}

		// Finalize streaming message
		statusText := "Calling tools..."
		if fullText != "" {
			statusText = fullText + "\n\nCalling tools..."
		}
		_ = updater.Finish(statusText)

		// Execute tools in parallel (LLM returned multiple independent tool_use blocks)
		a.dispatchToolsParallel(ctx, ch, sess, target, toolCalls, budgetWarning)
	}

	return nil
}

// unifiedNonStreaming handles the non-streaming fallback path.
func unifiedNonStreaming(ctx context.Context, a *Agent, ch channel.Channel, sess *session.Session, target channel.MessageTarget, systemPrompt string, maxIter int) error {
	for iteration := 0; iteration < maxIter; iteration++ {
		budgetWarning := computeBudgetPressure(iteration, maxIter, sess, systemPrompt, a.deps.Memory.ContextMgr)

		req := CompletionRequest{
			Model: a.deps.Core.LLMCfg.Model, System: systemPrompt,
			Messages: BuildMessages(sess), Tools: a.buildToolDefs(),
			MaxTokens: a.deps.Core.LLMCfg.MaxTokens,
		}

		resp, err := a.deps.Core.Provider.Complete(ctx, req)
		if err != nil && isContextLengthError(err) {
			if compErr := a.deps.Memory.ContextMgr.ReactiveCompress(ctx, sess, systemPrompt); compErr != nil {
				slog.Warn("reactive compress failed", "err", compErr)
			} else {
				a.eventBus.Publish(ContextCompressed{SessionID: sess.ID, Reason: "413_retry", LayersRun: 3})
				req.Messages = BuildMessages(sess)
				resp, err = a.deps.Core.Provider.Complete(ctx, req)
			}
		}
		if err != nil {
			return err
		}

		if resp.Text != "" {
			sess.AddMessage(session.Message{
				ID: fmt.Sprintf("msg_%d", time.Now().UnixNano()), Role: "assistant",
				Content: resp.Text, CreatedAt: time.Now(),
			})
		}
		for _, tc := range resp.ToolCalls {
			sess.AddMessage(session.Message{
				ID: tc.ID, Role: "tool_use", ToolName: tc.Name,
				ToolInput: tc.Input, CreatedAt: time.Now(),
			})
		}

		if len(resp.ToolCalls) == 0 {
			if sendErr := ch.Send(ctx, channel.OutboundMessage{
				Channel: target.Channel, ChannelID: target.ChannelID, Text: resp.Text,
			}); sendErr != nil {
				slog.Warn("failed to send message", "err", sendErr)
			}
			return nil
		}

		a.dispatchToolsParallel(ctx, ch, sess, target, resp.ToolCalls, budgetWarning)
	}
	return nil
}
```

- [ ] **Step 3: Verify compilation**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags "fts5" ./internal/agent/...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/unified_loop.go internal/agent/agent.go
git commit -m "feat: add UnifiedLoop with parallel tool dispatch
- dispatchToolsParallel: concurrent execution of independent tool_use blocks
- UnifiedLoop merges SimpleLoop logic with parallel dispatch
- Non-streaming fallback updated to use parallel dispatch"
```

---

### Task 4: Gateway Wiring — Register plan_task, Switch to UnifiedLoop

**Files:**
- Modify: `internal/gateway/init_agent.go`

- [ ] **Step 1: Update init_agent.go**

Replace the SimpleLoop instantiation and add plan_task registration:

```go
// internal/gateway/init_agent.go
package gateway

import (
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

func (gw *Gateway) initAgentRuntime() error {
	// LLM provider selection (unchanged)
	var provider agent.Provider
	switch gw.cfg.LLM.Provider {
	case "openai", "openai-compatible":
		provider = agent.NewOpenAIProvider(gw.cfg.LLM.APIKey, gw.cfg.LLM.Model, gw.cfg.LLM.BaseURL)
		slog.Info("LLM provider: openai-compatible", "model", gw.cfg.LLM.Model, "base_url", gw.cfg.LLM.BaseURL)
	default:
		provider = agent.NewClaudeProvider(gw.cfg.LLM.APIKey, gw.cfg.LLM.Model, gw.cfg.LLM.BaseURL)
		slog.Info("LLM provider: claude", "model", gw.cfg.LLM.Model)
	}

	if gw.cfg.LLM.Retry.MaxRetries > 0 {
		provider = agent.NewRetryProvider(provider, gw.cfg.LLM.Retry)
		slog.Info("LLM retry enabled", "max_retries", gw.cfg.LLM.Retry.MaxRetries, "base_delay", gw.cfg.LLM.Retry.BaseDelay)
	}
	gw.provider = provider

	getInterceptor := func() *tool.InterceptorChain {
		if gw.sandbox.InterceptorChain() != nil {
			return gw.sandbox.InterceptorChain()
		}
		return tool.NewInterceptorChain(nil)
	}

	deps := agent.AgentDeps{
		Core: agent.CoreDeps{
			Provider: gw.provider,
			Tools:    gw.tools,
			Sessions: gw.sessions,
			DB:       gw.db,
			Cfg:      gw.cfg.Agent,
			LLMCfg:   gw.cfg.LLM,
			AgentID:  "gateway",
			ToolsCfg: gw.cfg.Tools,
		},
		Memory: agent.MemoryDeps{
			ContextMgr: gw.contextMgr,
		}.WithDefaults(),
		Security: agent.SecurityDeps{
			Interceptor: getInterceptor(),
			HookMgr:     gw.hookMgr,
			PermEngine:  gw.permEngine,
		}.WithDefaults(),
		Observability: agent.ObservabilityDeps{
			Emitter:        gw.dashboard.Emitter(),
			MetricsEmitter: nil,
		}.WithDefaults(),
		MultiAgent: agent.MultiAgentDeps{
			ResultStore: gw.resultStore,
			SkillMgr:    gw.skillMgr,
		}.WithDefaults(),
	}

	if gw.cfg.Tools.ResultPersistence.Enabled {
		gw.resultStore = tool.NewResultStore(
			gw.cfg.Tools.ResultPersistence.CacheDir,
			gw.cfg.Tools.ResultPersistence.ThresholdBytes,
			gw.cfg.Tools.ResultPersistence.PreviewChars,
			gw.cfg.Tools.ResultPersistence.TTLHours,
		)
		deps.MultiAgent.ResultStore = gw.resultStore
		if err := gw.resultStore.Cleanup(); err != nil {
			slog.Warn("gateway: result store startup cleanup failed", "err", err)
		}
		slog.Info("tool result persistence enabled",
			"threshold", gw.cfg.Tools.ResultPersistence.ThresholdBytes,
			"ttl_hours", gw.cfg.Tools.ResultPersistence.TTLHours,
		)
	}

	gw.agentDeps = deps
	// UnifiedLoop is the sole execution strategy
	gw.agent = agent.NewAgent(deps.WithDefaults(), &agent.UnifiedLoop{}, agent.NewEventBus())
	gw.agent.SetApprovalFunc(gw.handleApproval)

	// Register plan_task tool so LLM can autonomously decompose complex tasks
	planExecutor := &agent.ToolExecutor{AgentRef: gw.agent}
	planTaskTool := tool.NewPlanTaskTool(
		gw.tools,
		gw.cfg.Agent.MaxParallelTools,
		planExecutor,
		gw.hookMgr,
		gw.permEngine,
		getInterceptor(),
	)
	gw.tools.Register(planTaskTool)
	slog.Info("plan_task tool registered")

	return nil
}
```

Note: `agent.ToolExecutor` is defined in Task 2, Step 2. It needs a public field `AgentRef *Agent`. Adjust the struct:

```go
// internal/agent/tool_bridge.go
type ToolExecutor struct {
	AgentRef *Agent
}

func (e *ToolExecutor) Execute(ctx context.Context, toolName, input string) (string, error) {
	if e.AgentRef == nil {
		return "", fmt.Errorf("ToolExecutor: AgentRef is nil")
	}
	call := &tool.ToolCall{ToolName: toolName, Input: input}
	
	t, err := e.AgentRef.deps.Core.Tools.Get(call.ToolName)
	if err != nil {
		return "", fmt.Errorf("tool not found: %s", call.ToolName)
	}
	
	result, err := e.AgentRef.deps.Security.Interceptor.Execute(ctx, call, func(ctx context.Context, call *tool.ToolCall) (*tool.ToolResult, error) {
		r, execErr := t.Execute(ctx, []byte(call.Input))
		if execErr != nil {
			return &tool.ToolResult{Error: execErr.Error()}, nil
		}
		return &tool.ToolResult{Output: r.Output}, nil
	})
	if err != nil {
		return "", err
	}
	if result.Error != "" {
		return "", fmt.Errorf(result.Error)
	}
	return result.Output, nil
}
```

- [ ] **Step 2: Verify compilation**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags "fts5" ./...
```

Expected: no errors. Fix any import issues.

- [ ] **Step 3: Commit**

```bash
git add internal/gateway/init_agent.go internal/agent/tool_bridge.go
git commit -m "feat: wire UnifiedLoop and plan_task tool in gateway
- Switch from SimpleLoop to UnifiedLoop as sole execution strategy
- Register plan_task tool with tool executor bridge
- LLM can now autonomously decompose complex tasks via plan_task"
```

---

### Task 5: Compression 5 Layers → 3 Layers

**Files:**
- Modify: `internal/agent/compression.go`
- Modify: `internal/agent/context_manager.go`

- [ ] **Step 1: Merge layers 0+1 into tool_output_reduce**

In `compression.go`, replace the `NewDefaultPipeline` function to build 3 layers instead of 5:

```go
// Replace NewDefaultPipeline in compression.go (~line 60-76)

func NewDefaultPipeline(provider Completer, store ResultStore, cfg CompressionConfig) *CompressionPipeline {
	layers := []CompressionLayer{
		// Layer 0: Reduce tool outputs — truncate large outputs + evict to store
		NewToolOutputReducer(store, ToolOutputReduceConfig{
			TruncateChars: 2000,
			EvictBytes:    8192,
			KeepLastTurns: 4,
		}),
		// Layer 1: Summarize old turns via LLM — preserves semantic meaning
		NewTurnSummarizer(provider, TurnSummarizerConfig{
			MaxTurnsToSummarize: 50,
		}),
		// Layer 2: Emergency truncation — soft trim first, then hard cut
		NewEmergencyTruncator(EmergencyTruncateConfig{
			SoftKeepTurns: 15,
			HardKeepTurns: 5,
		}),
	}
	return &CompressionPipeline{layers: layers, cfg: cfg}
}
```

Add new layer types at the bottom of `compression.go`:

```go
// ToolOutputReduceConfig configures the combined tool output reduction layer.
type ToolOutputReduceConfig struct {
	TruncateChars int // Truncate outputs > this many chars to 500-char preview
	EvictBytes    int // Evict outputs > this many bytes to ResultStore
	KeepLastTurns int // Never truncate/evict outputs from the last N turns
}

// ToolOutputReducer merges the former tool_output_prune and tool_eviction layers.
type ToolOutputReducer struct {
	store  ResultStore
	config ToolOutputReduceConfig
}

func NewToolOutputReducer(store ResultStore, cfg ToolOutputReduceConfig) *ToolOutputReducer {
	return &ToolOutputReducer{store: store, config: cfg}
}

func (r *ToolOutputReducer) Name() string { return "tool_output_reduce" }

func (r *ToolOutputReducer) Compress(ctx context.Context, msgs []CompletionMessage, utilization float64) ([]CompletionMessage, error) {
	// Only active above 60% utilization (was 40% for prune + 50% for eviction)
	if utilization < 0.60 {
		return msgs, nil
	}
	if len(msgs) <= r.config.KeepLastTurns*2 {
		return msgs, nil
	}

	keepFrom := len(msgs) - r.config.KeepLastTurns*2
	for i := 0; i < keepFrom; i++ {
		msg := &msgs[i]
		if msg.Role == "tool_result" {
			contentLen := len(msg.Content)
			if contentLen > r.config.EvictBytes && r.store != nil {
				// Evict large results to store
				r.store.Store(msg.Content)
				msg.Content = fmt.Sprintf("[tool result evicted, %d bytes]", contentLen)
			} else if contentLen > r.config.TruncateChars {
				// Truncate to preview
				msg.Content = msg.Content[:500] + fmt.Sprintf("\n...[truncated %d chars]", contentLen-500)
			}
		}
	}
	return ensurePairing(msgs), nil
}

// EmergencyTruncator merges old_context_removal and emergency_truncation.
type EmergencyTruncator struct {
	config EmergencyTruncateConfig
}

type EmergencyTruncateConfig struct {
	SoftKeepTurns int // Soft pass: keep this many most recent turns
	HardKeepTurns int // Hard pass: keep only this many turns
}

func NewEmergencyTruncator(cfg EmergencyTruncateConfig) *EmergencyTruncator {
	return &EmergencyTruncator{config: cfg}
}

func (e *EmergencyTruncator) Name() string { return "emergency_truncation" }

func (e *EmergencyTruncator) Compress(ctx context.Context, msgs []CompletionMessage, utilization float64) ([]CompletionMessage, error) {
	// Soft pass at 85%
	if utilization >= 0.85 && utilization < 0.95 {
		keep := e.config.SoftKeepTurns * 2 // 2 messages per turn (assistant + tool_result)
		if len(msgs) > keep {
			msgs = msgs[len(msgs)-keep:]
		}
		return ensurePairing(msgs), nil
	}
	// Hard pass at 95%+
	if utilization >= 0.95 {
		keep := e.config.HardKeepTurns * 2
		if len(msgs) > keep {
			msgs = msgs[len(msgs)-keep:]
		}
		return ensurePairing(msgs), nil
	}
	return msgs, nil
}
```

Add `"fmt"` to imports in compression.go.

Remove old layer types: `ToolOutputPruner`, `ToolEvictor`, `OldContextRemover`. Keep `TurnSummarizer` unchanged.

- [ ] **Step 2: Update context_manager.go references**

In `context_manager.go`, update any references to the old layer count. Search for `LayersRun: 5` and change to `LayersRun: 3`. Also update the `NewDefaultPipeline` call if the signature changed:

```bash
# Find all references to LayersRun: 5
grep -rn "LayersRun: 5\|LayersRun:5" internal/
```

Change all found instances to `LayersRun: 3`.

- [ ] **Step 3: Run compression tests**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go test -tags "fts5" ./internal/agent/ -run TestCompress -v -count=1
```

If existing tests reference removed layer types, update them to use the new merged types.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/compression.go internal/agent/context_manager.go
git commit -m "refactor: collapse compression pipeline 5→3 layers
- Merge tool_output_prune + tool_eviction → tool_output_reduce
- Merge old_context_removal + emergency_truncation → emergency_truncator  
- Keep turn_summarization (LLM-based, genuinely valuable)
- Remove ensureToolPairing dependency in new layers
- Update LayersRun: 5 → 3 in all call sites"
```

---

### Task 6: Delete Cognitive Loop Files

**Files to DELETE:**
- `internal/agent/cognitive_loop.go`
- `internal/agent/perceive.go`
- `internal/agent/plan.go`
- `internal/agent/observe.go`
- `internal/agent/reflect.go`
- `internal/agent/loop_strategy.go`
- `internal/agent/cognitive_types.go`
- `internal/agent/assertion.go`
- `internal/agent/failure_context.go`
- `internal/agent/checkpoint.go`
- `internal/agent/context_budget.go`
- `internal/agent/tool_cache.go`
- `internal/gateway/init_cognitive.go`
- `internal/gateway/subsystem_evolution.go`

**Files to KEEP but clean up:**
- `internal/agent/plan_mode.go` — PlanMode struct may still be referenced; keep, mark as deprecated
- `internal/agent/project_scanner.go` — Still used by assembleContext
- `internal/agent/git_context.go` — Still used by assembleContext

- [ ] **Step 1: Check for remaining references**

```bash
# Find any imports of deleted files
grep -rn "cognitive_loop\|Perceiver\|Planner\|Observer\|Reflector\|CognitiveLoop\|loop_strategy\|cognitive_types\|assertion\.go\|failure_context\|checkpoint\.go\|context_budget\|tool_cache" internal/ --include="*.go" | grep -v "_test.go" | grep -v "\.git"
```

Expected: find all references that need cleanup before deletion.

- [ ] **Step 2: Clean up gateway references**

Remove imports and references in `internal/gateway/gateway.go`:
- Remove `init_cognitive.go` import side effects
- Remove `subsystem_evolution.go` if it exists

Remove cognitive-related fields from the Gateway struct if any remain.

- [ ] **Step 3: Clean up agent package references**

In `internal/agent/agent.go`:
- Remove `LoopStrategy` related code (SetStrategy, Strategy() methods)
- Remove cognitive-related imports
- Keep `SimpleLoop` temporarily (it's renamed to `UnifiedLoop` now)

- [ ] **Step 4: Delete the files**

```bash
cd /Users/wuqisen/dev/IronClaw
rm internal/agent/cognitive_loop.go
rm internal/agent/perceive.go
rm internal/agent/plan.go
rm internal/agent/observe.go
rm internal/agent/reflect.go
rm internal/agent/loop_strategy.go
rm internal/agent/cognitive_types.go
rm internal/agent/assertion.go
rm internal/agent/failure_context.go
rm internal/agent/checkpoint.go
rm internal/agent/context_budget.go
rm internal/agent/tool_cache.go
rm internal/gateway/init_cognitive.go
rm internal/gateway/subsystem_evolution.go
```

- [ ] **Step 5: Verify compilation after deletion**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags "fts5" ./...
```

Fix any broken references. If `PlanMode` is still imported elsewhere, keep `plan_mode.go`; if not, delete it too.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: delete cognitive loop — 14 files, ~3,500 LOC removed
- Removed: cognitive_loop, perceive, plan, observe, reflect, loop_strategy
- Removed: cognitive_types, assertion, failure_context, checkpoint, context_budget, tool_cache
- Removed: gateway init_cognitive, subsystem_evolution
- UnifiedLoop is now the sole execution strategy
- plan_task tool provides LLM-driven task decomposition"
```

---

### Task 7: Cleanup — Dead Code and Unused Imports

**Files:**
- Delete: `internal/gateway/command_feature.go` (245 lines, dead duplicate)
- Modify: various files with stale imports

- [ ] **Step 1: Delete command_feature.go**

```bash
rm /Users/wuqisen/dev/IronClaw/internal/gateway/command_feature.go
```

This file duplicates `/feature`, `/config`, `/compact`, `/model` handlers from `commands.go`. Only `commands.go` versions are wired in the command table.

- [ ] **Step 2: Run goimports to clean unused imports**

```bash
cd /Users/wuqisen/dev/IronClaw
goimports -w internal/gateway/*.go internal/agent/*.go
```

- [ ] **Step 3: Verify tests still pass**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go test -tags "fts5" ./internal/agent/... ./internal/dag/... ./internal/tool/... ./internal/gateway/... -count=1
```

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "chore: remove dead code — command_feature.go, unused imports
- command_feature.go: 245-line duplicate of commands.go handlers, never dispatched
- Clean stale imports after cognitive loop deletion
- All agent/dag/tool/gateway tests passing"
```

---

### Task 8: Integration Test — End-to-End with Mock LLM

**Files:**
- Create: `internal/agent/unified_loop_test.go`

- [ ] **Step 1: Write integration test**

```go
// internal/agent/unified_loop_test.go
package agent

import (
	"context"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// mockChannel implements channel.Channel for testing
type mockChannel struct{}

func (m *mockChannel) Send(ctx context.Context, msg channel.OutboundMessage) error { return nil }
func (m *mockChannel) SendStreaming(ctx context.Context, target channel.MessageTarget) (channel.StreamingUpdater, error) {
	return &mockUpdater{}, nil
}
func (m *mockChannel) Name() string { return "mock" }

type mockUpdater struct{}

func (m *mockUpdater) Update(text string) error  { return nil }
func (m *mockUpdater) Finish(text string) error  { return nil }

// mockProvider returns pre-scripted responses
type mockProvider struct {
	responses []CompletionResponse
	callCount int
}

func (m *mockProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	if m.callCount < len(m.responses) {
		r := m.responses[m.callCount]
		m.callCount++
		return &r, nil
	}
	return &CompletionResponse{Text: "done"}, nil
}

func (m *mockProvider) Stream(ctx context.Context, req CompletionRequest) (CompletionStream, error) {
	return &mockStream{resp: m.responses[m.callCount]}, nil
}

type mockStream struct {
	resp CompletionResponse
	done bool
}

func (m *mockStream) Next() (CompletionDelta, error) {
	if !m.done {
		m.done = true
		delta := CompletionDelta{Done: true, StopReason: "end_turn"}
		delta.ToolCalls = m.resp.ToolCalls
		delta.Text = m.resp.Text
		return delta, nil
	}
	return CompletionDelta{}, io.EOF
}

func (m *mockStream) Close() error { return nil }

func TestUnifiedLoopSingleToolCall(t *testing.T) {
	// Setup
	mgr := session.NewManager()
	sess := mgr.CreateSession("test", "test-user")
	
	registry := tool.NewRegistry()
	registry.Register(&mockReadTool{})
	
	deps := AgentDeps{}.WithDefaults()
	deps.Core.Tools = registry
	deps.Core.Sessions = mgr
	deps.Core.Cfg.MaxIterations = 3
	
	provider := &mockProvider{
		responses: []CompletionResponse{
			{
				Text: "Let me read that file.",
				ToolCalls: []ToolUseBlock{
					{ID: "call_1", Name: "read", Input: `{"path":"/tmp/test"}`},
				},
			},
		},
	}
	deps.Core.Provider = provider
	
	a := NewAgent(deps, &UnifiedLoop{}, NewEventBus())
	ch := &mockChannel{}
	
	err := a.HandleMessage(context.Background(), ch, channel.InboundMessage{
		Channel: "mock", ChannelID: "ch1", Text: "read /tmp/test",
	})
	
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify session has the tool call and result
	msgs := sess.Messages()
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(msgs))
	}
}

type mockReadTool struct{}

func (m *mockReadTool) Name() string              { return "read" }
func (m *mockReadTool) Description() string       { return "Read a file" }
func (m *mockReadTool) InputSchema() string       { return `{"type":"object"}` }
func (m *mockReadTool) IsReadOnly() bool          { return true }
func (m *mockReadTool) RequiresApproval() bool    { return false }
func (m *mockReadTool) Execute(ctx context.Context, input []byte) (*tool.Result, error) {
	return &tool.Result{Output: "file contents"}, nil
}
```

Add `"io"` to imports.

- [ ] **Step 2: Run integration test**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go test -tags "fts5" ./internal/agent/ -run TestUnifiedLoop -v -count=1
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/unified_loop_test.go
git commit -m "test: add UnifiedLoop integration test with mock LLM
- Verifies single tool call flow: LLM → tool dispatch → result → done
- Mock channel + mock provider for deterministic testing"
```

---

### Final Verification

```bash
cd /Users/wuqisen/dev/IronClaw
CGO_ENABLED=1 go build -tags "fts5" ./...
CGO_ENABLED=1 go test -tags "fts5" ./internal/agent/... ./internal/dag/... ./internal/tool/... ./internal/gateway/... -count=1
make lint
```

Expected: clean build, all tests pass, no lint errors.
