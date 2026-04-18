# Agent Reliability Improvement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the reliability gap with frontier agent projects by adding structured verification, smart retry, tool output quality, and context intelligence to IronClaw's cognitive agent loop.

**Architecture:** The work spans three independent tracks (A: long-task reliability, B: tool quality, C: context intelligence) executed in four phases. Phase ordering respects cross-track dependencies: Phase 1 lays assertion/structured-output foundations, Phase 2 builds on them for smart retry + project awareness, Phase 3 adds browser tools + caching + git, Phase 4 adds checkpoint/resume and dynamic budgeting. All changes target the cognitive agent path (`internal/agent/`) and tool layer (`internal/tool/`).

**Tech Stack:** Go 1.22+, SQLite (mattn/go-sqlite3 with FTS5), standard library `os/exec`, `net/http`, `crypto/sha256`. No new external dependencies.

**Spec:** `docs/superpowers/specs/2026-04-18-agent-reliability-improvement-design.md`

---

## File Structure

### New Files

| File | Responsibility |
|------|---------------|
| `internal/agent/assertion.go` | `AssertionResult` generation logic — auto-generates assertions per tool type |
| `internal/agent/assertion_test.go` | Unit tests for assertion generation |
| `internal/agent/failure_context.go` | `FailureContext` struct + builder from `ObservationResult` |
| `internal/agent/failure_context_test.go` | Unit tests for failure context building |
| `internal/agent/project_scanner.go` | `ProjectContextScanner` — detects project type, build commands, key directories |
| `internal/agent/project_scanner_test.go` | Unit tests for project scanning |
| `internal/agent/git_context.go` | `GitContextProvider` — collects branch, uncommitted files, recent commits |
| `internal/agent/git_context_test.go` | Unit tests for git context |
| `internal/agent/context_budget.go` | `ContextBudgetAllocator` — complexity-aware context allocation |
| `internal/agent/context_budget_test.go` | Unit tests for budget allocation |
| `internal/agent/tool_cache.go` | `ToolResultCache` — per-task cache for read-only tool results |
| `internal/agent/tool_cache_test.go` | Unit tests for tool cache |
| `internal/agent/checkpoint.go` | `CheckpointStore` interface + `SQLiteCheckpointStore` implementation |
| `internal/agent/checkpoint_test.go` | Unit tests for checkpoint store |
| `internal/tool/browser_search.go` | `BrowserSearchTool` — structured search results |
| `internal/tool/browser_search_test.go` | Unit tests for browser search |
| `internal/tool/browser_extract.go` | `BrowserExtractTool` — Readability-style Markdown extraction |
| `internal/tool/browser_extract_test.go` | Unit tests for browser extract |
| `internal/store/migrations/018_task_checkpoints.sql` | DDL for `task_checkpoints` table |

### Modified Files

| File | Changes |
|------|---------|
| `internal/agent/cognitive_types.go` | Add `AssertionResult`, `FailureContext`, `ProjectContext` types; extend `ObservationResult` and `CognitiveState` |
| `internal/agent/observe.go` | Integrate assertion generation into `Observer.Run` |
| `internal/agent/reflect.go` | Accept `FailureContext` in `buildReflectUserMessage`; serialize into replan prompt |
| `internal/agent/cognitive_prompts.go` | Add `{{PROJECT_CONTEXT}}` and `{{GIT_STATE}}` placeholders to `PlanUserPromptTemplate` |
| `internal/agent/plan.go` | Substitute new placeholders in `buildPlanUserMessage` |
| `internal/agent/perceive.go` | Call `ProjectContextScanner` and `GitContextProvider`; wire `ContextBudgetAllocator` |
| `internal/agent/cognitive.go` | Wire checkpoint save/load; pass `FailureContext` through replan loop |
| `internal/tool/bash.go` | Return structured JSON in `Result.Output`; large output → temp file |
| `internal/agent/act.go` | Integrate `ToolResultCache` into `executeSubTask` |
| `internal/gateway/init_tools.go` | Register `browser_search` and `browser_extract` tools |

---

## Phase 1: Assertion Loop + Structured Bash

### Task 1: Assertion Types (A2 — types only)

**Files:**
- Modify: `internal/agent/cognitive_types.go:89-108`

- [ ] **Step 1: Add `AssertionResult` and `FailureContext` types to `cognitive_types.go`**

After the `Observation` struct (line 99) and before `ObservationResult` (line 101), add:

```go
// AssertionResult records a single verification check on a tool execution.
type AssertionResult struct {
	Check   string // human-readable description, e.g. "exit_code == 0"
	Passed  bool
	Actual  string // what was observed, e.g. "exit_code = 1"
}

// FailureContext provides structured context about a subtask failure for replan prompts.
type FailureContext struct {
	SubTaskID    string
	ToolName     string
	ErrorType    string // "assertion_failed", "tool_error", "denied"
	ErrorMsg     string
	AttemptCount int
	Assertions   []AssertionResult
}
```

Extend `ObservationResult` by adding two new fields at the end of the struct:

```go
type ObservationResult struct {
	Observations    []Observation
	SuccessCount    int
	FailureCount    int
	DeniedCount     int
	OverallProgress float64
	ErrorPatterns   []string
	Assertions      []AssertionResult // per-observation assertion results
	Failures        []FailureContext  // structured failure context for replan
}
```

- [ ] **Step 2: Verify compilation**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./internal/agent/`
Expected: clean build (no errors)

- [ ] **Step 3: Commit**

```bash
git add internal/agent/cognitive_types.go
git commit -m "feat(agent): add AssertionResult, FailureContext types and extend ObservationResult"
```

---

### Task 2: Assertion Generator (A2 — logic)

**Files:**
- Create: `internal/agent/assertion.go`
- Test: `internal/agent/assertion_test.go`

- [ ] **Step 1: Write the failing test for bash assertion generation**

Create `internal/agent/assertion_test.go`:

```go
package agent

import (
	"testing"
)

func TestGenerateAssertions_Bash_Success(t *testing.T) {
	obs := Observation{
		SubTaskID: "1",
		ToolName:  "bash",
		Output:    `{"stdout":"hello","stderr":"","exit_code":0,"status":"ok"}`,
		Error:     "",
	}
	results := generateAssertions(obs)

	if len(results) == 0 {
		t.Fatal("expected at least one assertion")
	}

	var foundExitCode bool
	for _, r := range results {
		if r.Check == "exit_code == 0" {
			foundExitCode = true
			if !r.Passed {
				t.Errorf("exit_code assertion should pass, actual=%q", r.Actual)
			}
		}
	}
	if !foundExitCode {
		t.Error("expected exit_code assertion")
	}
}

func TestGenerateAssertions_Bash_Failed(t *testing.T) {
	obs := Observation{
		SubTaskID: "2",
		ToolName:  "bash",
		Output:    `{"stdout":"","stderr":"command not found","exit_code":127,"status":"failed"}`,
		Error:     "",
	}
	results := generateAssertions(obs)

	var exitCodeAssertion *AssertionResult
	for i := range results {
		if results[i].Check == "exit_code == 0" {
			exitCodeAssertion = &results[i]
		}
	}
	if exitCodeAssertion == nil {
		t.Fatal("expected exit_code assertion")
	}
	if exitCodeAssertion.Passed {
		t.Error("exit_code assertion should fail for code 127")
	}

	var stderrAssertion *AssertionResult
	for i := range results {
		if results[i].Check == "stderr has no error keywords" {
			stderrAssertion = &results[i]
		}
	}
	if stderrAssertion == nil {
		t.Fatal("expected stderr assertion")
	}
	if stderrAssertion.Passed {
		t.Error("stderr assertion should fail when stderr contains 'not found'")
	}
}

func TestGenerateAssertions_HTTP_Success(t *testing.T) {
	obs := Observation{
		SubTaskID: "3",
		ToolName:  "http",
		Output:    `{"status_code":200,"body":"ok"}`,
		Error:     "",
	}
	results := generateAssertions(obs)

	var found bool
	for _, r := range results {
		if r.Check == "status_code < 400" {
			found = true
			if !r.Passed {
				t.Errorf("status_code assertion should pass for 200")
			}
		}
	}
	if !found {
		t.Error("expected status_code assertion")
	}
}

func TestGenerateAssertions_HTTP_ServerError(t *testing.T) {
	obs := Observation{
		SubTaskID: "4",
		ToolName:  "http",
		Output:    `{"status_code":500,"body":"internal error"}`,
		Error:     "",
	}
	results := generateAssertions(obs)

	for _, r := range results {
		if r.Check == "status_code < 400" {
			if r.Passed {
				t.Error("status_code assertion should fail for 500")
			}
			return
		}
	}
	t.Error("expected status_code assertion")
}

func TestGenerateAssertions_UnknownTool(t *testing.T) {
	obs := Observation{
		SubTaskID: "5",
		ToolName:  "custom_tool",
		Output:    "some output",
	}
	results := generateAssertions(obs)
	if len(results) != 0 {
		t.Errorf("expected no assertions for unknown tool, got %d", len(results))
	}
}

func TestGenerateAssertions_DeniedObservation(t *testing.T) {
	obs := Observation{
		SubTaskID: "6",
		ToolName:  "bash",
		Denied:    true,
	}
	results := generateAssertions(obs)
	if len(results) != 0 {
		t.Errorf("expected no assertions for denied observation, got %d", len(results))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestGenerateAssertions ./internal/agent/ -v`
Expected: FAIL — `generateAssertions` undefined

- [ ] **Step 3: Write the assertion generator implementation**

Create `internal/agent/assertion.go`:

```go
package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

var stderrErrorKeywords = []string{
	"error", "fatal", "panic", "not found", "permission denied",
	"segfault", "traceback", "exception",
}

// generateAssertions produces verification checks for a single observation
// based on the tool type. Returns nil for unrecognized tools or denied observations.
func generateAssertions(obs Observation) []AssertionResult {
	if obs.Denied {
		return nil
	}

	switch obs.ToolName {
	case "bash":
		return bashAssertions(obs)
	case "http":
		return httpAssertions(obs)
	case "file_write", "file_edit":
		return fileWriteAssertions(obs)
	default:
		return nil
	}
}

func bashAssertions(obs Observation) []AssertionResult {
	var parsed struct {
		ExitCode int    `json:"exit_code"`
		Stderr   string `json:"stderr"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal([]byte(obs.Output), &parsed); err != nil {
		if obs.Error != "" {
			return []AssertionResult{{
				Check:  "exit_code == 0",
				Passed: false,
				Actual: fmt.Sprintf("tool error: %s", obs.Error),
			}}
		}
		return nil
	}

	var results []AssertionResult

	results = append(results, AssertionResult{
		Check:  "exit_code == 0",
		Passed: parsed.ExitCode == 0,
		Actual: fmt.Sprintf("exit_code = %d", parsed.ExitCode),
	})

	stderrLower := strings.ToLower(parsed.Stderr)
	hasErrorKeyword := false
	for _, kw := range stderrErrorKeywords {
		if strings.Contains(stderrLower, kw) {
			hasErrorKeyword = true
			break
		}
	}
	results = append(results, AssertionResult{
		Check:  "stderr has no error keywords",
		Passed: !hasErrorKeyword,
		Actual: truncate(parsed.Stderr, 200),
	})

	return results
}

func httpAssertions(obs Observation) []AssertionResult {
	var parsed struct {
		StatusCode int `json:"status_code"`
	}
	if err := json.Unmarshal([]byte(obs.Output), &parsed); err != nil {
		return nil
	}

	return []AssertionResult{{
		Check:  "status_code < 400",
		Passed: parsed.StatusCode < 400,
		Actual: fmt.Sprintf("status_code = %d", parsed.StatusCode),
	}}
}

func fileWriteAssertions(obs Observation) []AssertionResult {
	if obs.Error != "" {
		return []AssertionResult{{
			Check:  "file operation succeeded",
			Passed: false,
			Actual: fmt.Sprintf("error: %s", truncate(obs.Error, 200)),
		}}
	}
	return []AssertionResult{{
		Check:  "file operation succeeded",
		Passed: true,
		Actual: "no error",
	}}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestGenerateAssertions ./internal/agent/ -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/assertion.go internal/agent/assertion_test.go
git commit -m "feat(agent): implement assertion generator for bash, http, file_write tools"
```

---

### Task 3: Integrate Assertions into Observer (A2 — wiring)

**Files:**
- Modify: `internal/agent/observe.go:14-47`

- [ ] **Step 1: Write a test for Observer.Run with assertions**

Add to `internal/agent/assertion_test.go`:

```go
func TestObserverRun_PopulatesAssertions(t *testing.T) {
	obs := NewObserver()
	plan := &TaskPlan{
		SubTasks: []*SubTask{
			{ID: "1", ToolName: "bash", Status: SubTaskDone},
			{ID: "2", ToolName: "http", Status: SubTaskDone},
		},
	}
	observations := []Observation{
		{SubTaskID: "1", ToolName: "bash", Output: `{"stdout":"ok","stderr":"","exit_code":0,"status":"ok"}`},
		{SubTaskID: "2", ToolName: "http", Output: `{"status_code":500,"body":"err"}`},
	}

	result := obs.Run(observations, plan)

	if len(result.Assertions) == 0 {
		t.Fatal("expected assertions to be populated")
	}
	if len(result.Failures) == 0 {
		t.Fatal("expected at least one failure context (http 500)")
	}

	var httpFailure *FailureContext
	for i := range result.Failures {
		if result.Failures[i].SubTaskID == "2" {
			httpFailure = &result.Failures[i]
		}
	}
	if httpFailure == nil {
		t.Fatal("expected failure context for subtask 2")
	}
	if httpFailure.ErrorType != "assertion_failed" {
		t.Errorf("ErrorType = %q, want %q", httpFailure.ErrorType, "assertion_failed")
	}
}

func TestObserverRun_NoAssertionFailures(t *testing.T) {
	obs := NewObserver()
	plan := &TaskPlan{
		SubTasks: []*SubTask{
			{ID: "1", ToolName: "bash", Status: SubTaskDone},
		},
	}
	observations := []Observation{
		{SubTaskID: "1", ToolName: "bash", Output: `{"stdout":"hello","stderr":"","exit_code":0,"status":"ok"}`},
	}

	result := obs.Run(observations, plan)

	if len(result.Failures) != 0 {
		t.Errorf("expected no failures, got %d", len(result.Failures))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run "TestObserverRun_PopulatesAssertions|TestObserverRun_NoAssertionFailures" ./internal/agent/ -v`
Expected: FAIL — `result.Assertions` is nil (field exists from Task 1 but never populated)

- [ ] **Step 3: Modify `observe.go` to generate assertions and build failure contexts**

In `internal/agent/observe.go`, replace the `Run` method body (lines 14–47):

```go
func (o *Observer) Run(observations []Observation, plan *TaskPlan) *ObservationResult {
	result := &ObservationResult{
		Observations: observations,
	}

	skippedCount := 0
	for _, st := range plan.SubTasks {
		if st.Status == SubTaskSkipped {
			skippedCount++
		}
	}

	for _, obs := range observations {
		if obs.Denied {
			result.DeniedCount++
			result.Failures = append(result.Failures, FailureContext{
				SubTaskID: obs.SubTaskID,
				ToolName:  obs.ToolName,
				ErrorType: "denied",
				ErrorMsg:  "tool execution was denied",
			})
			continue
		}

		assertions := generateAssertions(obs)
		result.Assertions = append(result.Assertions, assertions...)

		anyFailed := false
		for _, a := range assertions {
			if !a.Passed {
				anyFailed = true
				break
			}
		}

		if obs.Error != "" {
			result.FailureCount++
			result.Failures = append(result.Failures, FailureContext{
				SubTaskID:  obs.SubTaskID,
				ToolName:   obs.ToolName,
				ErrorType:  "tool_error",
				ErrorMsg:   obs.Error,
				Assertions: assertions,
			})
		} else if anyFailed {
			result.FailureCount++
			failedChecks := make([]string, 0)
			for _, a := range assertions {
				if !a.Passed {
					failedChecks = append(failedChecks, a.Check+": "+a.Actual)
				}
			}
			result.Failures = append(result.Failures, FailureContext{
				SubTaskID:  obs.SubTaskID,
				ToolName:   obs.ToolName,
				ErrorType:  "assertion_failed",
				ErrorMsg:   strings.Join(failedChecks, "; "),
				Assertions: assertions,
			})
		} else {
			result.SuccessCount++
		}
	}

	result.ErrorPatterns = o.detectErrorPatterns(observations)

	totalSubTasks := len(plan.SubTasks)
	effective := totalSubTasks - skippedCount
	if effective > 0 {
		result.OverallProgress = float64(result.SuccessCount) / float64(effective)
	}

	return result
}
```

Add `"strings"` to the import list in `observe.go` if not already present.

- [ ] **Step 4: Run all tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run "TestObserverRun|TestGenerateAssertions" ./internal/agent/ -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/observe.go internal/agent/assertion_test.go
git commit -m "feat(agent): integrate assertion generator into Observer.Run with failure context"
```

---

### Task 4: Structured Bash Output (B1)

**Files:**
- Modify: `internal/tool/bash.go:12-107`
- Create: `internal/tool/bash_test.go`

- [ ] **Step 1: Write the failing test for structured bash output**

Create `internal/tool/bash_test.go`:

```go
package tool

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type bashResult struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	Truncated  bool   `json:"truncated"`
	DurationMs int64  `json:"duration_ms"`
	Status     string `json:"status"`
}

func TestBashTool_StructuredOutput_Success(t *testing.T) {
	bt := NewBashTool(10*time.Second, false, NewPolicy(nil))
	input, _ := json.Marshal(bashInput{Command: "echo hello"})

	result, err := bt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}

	var parsed bashResult
	if err := json.Unmarshal([]byte(result.Output), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, result.Output)
	}

	if parsed.Status != "ok" {
		t.Errorf("status = %q, want %q", parsed.Status, "ok")
	}
	if parsed.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", parsed.ExitCode)
	}
	if parsed.Stdout != "hello\n" {
		t.Errorf("stdout = %q, want %q", parsed.Stdout, "hello\n")
	}
	if parsed.DurationMs <= 0 {
		t.Errorf("duration_ms should be positive, got %d", parsed.DurationMs)
	}
}

func TestBashTool_StructuredOutput_Failure(t *testing.T) {
	bt := NewBashTool(10*time.Second, false, NewPolicy(nil))
	input, _ := json.Marshal(bashInput{Command: "exit 42"})

	result, err := bt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed bashResult
	if err := json.Unmarshal([]byte(result.Output), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, result.Output)
	}

	if parsed.Status != "failed" {
		t.Errorf("status = %q, want %q", parsed.Status, "failed")
	}
	if parsed.ExitCode != 42 {
		t.Errorf("exit_code = %d, want 42", parsed.ExitCode)
	}
}

func TestBashTool_StructuredOutput_Stderr(t *testing.T) {
	bt := NewBashTool(10*time.Second, false, NewPolicy(nil))
	input, _ := json.Marshal(bashInput{Command: "echo out && echo err >&2"})

	result, err := bt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed bashResult
	if err := json.Unmarshal([]byte(result.Output), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, result.Output)
	}

	if parsed.Stdout != "out\n" {
		t.Errorf("stdout = %q, want %q", parsed.Stdout, "out\n")
	}
	if parsed.Stderr != "err\n" {
		t.Errorf("stderr = %q, want %q", parsed.Stderr, "err\n")
	}
}

func TestBashTool_Timeout(t *testing.T) {
	bt := NewBashTool(100*time.Millisecond, false, NewPolicy(nil))
	input, _ := json.Marshal(bashInput{Command: "sleep 10"})

	result, err := bt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected timeout error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestBashTool_Structured ./internal/tool/ -v`
Expected: FAIL — output is plain text, not JSON

- [ ] **Step 3: Implement structured bash output**

Replace the `Execute` method in `internal/tool/bash.go` (lines 64–107):

```go
const largeOutputThreshold = 8 * 1024 // 8KB — outputs larger than this are written to temp file

type bashOutput struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	Truncated  bool   `json:"truncated"`
	DurationMs int64  `json:"duration_ms"`
	Status     string `json:"status"`
	FilePath   string `json:"file_path,omitempty"`
}

func (b *BashTool) Execute(ctx context.Context, input []byte) (Result, error) {
	var in bashInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: "invalid input: " + err.Error()}, nil
	}

	if in.Command == "" {
		return Result{Error: "command is required"}, nil
	}

	if msg := b.policy.CheckBashCommand(in.Command); msg != "" {
		return Result{Error: msg}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, "bash", "-c", in.Command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	durationMs := time.Since(start).Milliseconds()

	out := bashOutput{
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMs: durationMs,
	}

	if ctx.Err() == context.DeadlineExceeded {
		return Result{Error: fmt.Sprintf("command timed out after %s", b.timeout)}, nil
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			out.ExitCode = exitErr.ExitCode()
		} else {
			out.ExitCode = -1
		}
		out.Status = "failed"
	} else {
		out.ExitCode = 0
		out.Status = "ok"
	}

	truncated := false
	if len(out.Stdout) > maxOutputSize {
		out.Stdout = out.Stdout[:maxOutputSize]
		truncated = true
	}
	if len(out.Stderr) > maxOutputSize {
		out.Stderr = out.Stderr[:maxOutputSize]
		truncated = true
	}
	out.Truncated = truncated

	resultJSON, _ := json.Marshal(out)
	outputStr := string(resultJSON)

	var filePath string
	if len(outputStr) > largeOutputThreshold {
		tmpFile, writeErr := os.CreateTemp("", "ironclaw-bash-*.json")
		if writeErr == nil {
			_, _ = tmpFile.Write(resultJSON)
			_ = tmpFile.Close()
			filePath = tmpFile.Name()

			summary := bashOutput{
				Stdout:     truncateStr(out.Stdout, 500),
				Stderr:     truncateStr(out.Stderr, 200),
				ExitCode:   out.ExitCode,
				Truncated:  true,
				DurationMs: out.DurationMs,
				Status:     out.Status,
				FilePath:   filePath,
			}
			resultJSON, _ = json.Marshal(summary)
			outputStr = string(resultJSON)
		}
	}

	r := Result{
		Output:   outputStr,
		FilePath: filePath,
		Metadata: map[string]any{
			"exit_code":   out.ExitCode,
			"status":      out.Status,
			"duration_ms": out.DurationMs,
		},
	}
	if truncated {
		r.IsPartial = true
	}
	if out.Status == "failed" {
		r.Error = fmt.Sprintf("exit code %d", out.ExitCode)
	}
	return r, nil
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...[truncated]"
}
```

Add `"os"` to the import list in `bash.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestBashTool ./internal/tool/ -v`
Expected: all PASS

- [ ] **Step 5: Build the full project to verify no regressions**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: clean build

- [ ] **Step 6: Commit**

```bash
git add internal/tool/bash.go internal/tool/bash_test.go
git commit -m "feat(tool): structured JSON output for bash tool with temp file for large output"
```

---

## Phase 2: Smart Retry + Project Context

### Task 5: Failure Context Builder (A3 — types + builder)

**Files:**
- Create: `internal/agent/failure_context.go`
- Create: `internal/agent/failure_context_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/agent/failure_context_test.go`:

```go
package agent

import (
	"testing"
)

func TestBuildFailureContexts_FromObservationResult(t *testing.T) {
	obsResult := &ObservationResult{
		Failures: []FailureContext{
			{SubTaskID: "1", ToolName: "bash", ErrorType: "assertion_failed", ErrorMsg: "exit_code = 1"},
			{SubTaskID: "1", ToolName: "bash", ErrorType: "assertion_failed", ErrorMsg: "exit_code = 1"},
			{SubTaskID: "1", ToolName: "bash", ErrorType: "assertion_failed", ErrorMsg: "exit_code = 1"},
		},
	}

	enriched := enrichFailureContexts(obsResult.Failures, 2)

	if len(enriched) != 3 {
		t.Fatalf("expected 3 failures, got %d", len(enriched))
	}
	for _, f := range enriched {
		if f.AttemptCount != 2 {
			t.Errorf("AttemptCount = %d, want 2", f.AttemptCount)
		}
	}
}

func TestFormatFailureContextForPrompt(t *testing.T) {
	failures := []FailureContext{
		{
			SubTaskID:    "1",
			ToolName:     "bash",
			ErrorType:    "assertion_failed",
			ErrorMsg:     "exit_code = 127",
			AttemptCount: 2,
		},
	}

	prompt := formatFailureContextForPrompt(failures)

	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
	if !contains(prompt, "bash") {
		t.Error("prompt should mention tool name")
	}
	if !contains(prompt, "127") {
		t.Error("prompt should include error details")
	}
	if !contains(prompt, "attempt 2") {
		t.Error("prompt should mention attempt count")
	}
}

func TestShouldDegradeRetry(t *testing.T) {
	failures := []FailureContext{
		{SubTaskID: "1", ToolName: "bash", ErrorType: "assertion_failed", AttemptCount: 3},
	}
	if !shouldDegradeRetry(failures) {
		t.Error("expected degradation after 3 same-type failures")
	}

	failures2 := []FailureContext{
		{SubTaskID: "1", ToolName: "bash", ErrorType: "assertion_failed", AttemptCount: 1},
	}
	if shouldDegradeRetry(failures2) {
		t.Error("should not degrade after only 1 attempt")
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && containsStr(s, substr)
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run "TestBuildFailureContexts|TestFormatFailureContext|TestShouldDegradeRetry" ./internal/agent/ -v`
Expected: FAIL — functions undefined

- [ ] **Step 3: Implement the failure context builder**

Create `internal/agent/failure_context.go`:

```go
package agent

import (
	"fmt"
	"strings"
)

const degradeThreshold = 3

// enrichFailureContexts sets the AttemptCount on each failure.
func enrichFailureContexts(failures []FailureContext, replanAttempt int) []FailureContext {
	enriched := make([]FailureContext, len(failures))
	copy(enriched, failures)
	for i := range enriched {
		enriched[i].AttemptCount = replanAttempt
	}
	return enriched
}

// formatFailureContextForPrompt serializes failure contexts into a human-readable
// string suitable for injection into the REFLECT re-plan prompt.
func formatFailureContextForPrompt(failures []FailureContext) string {
	if len(failures) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("FAILURE CONTEXT (structured):\n")
	for _, f := range failures {
		fmt.Fprintf(&sb, "- SubTask %s [%s] (attempt %d): %s — %s\n",
			f.SubTaskID, f.ToolName, f.AttemptCount, f.ErrorType, f.ErrorMsg)
		for _, a := range f.Assertions {
			status := "PASS"
			if !a.Passed {
				status = "FAIL"
			}
			fmt.Fprintf(&sb, "    [%s] %s → %s\n", status, a.Check, a.Actual)
		}
	}

	if shouldDegradeRetry(failures) {
		sb.WriteString("\nWARNING: Multiple failures of the same type detected. ")
		sb.WriteString("Consider using a more conservative approach ")
		sb.WriteString("(e.g. file_write instead of bash echo, simpler commands).\n")
	}

	return sb.String()
}

// shouldDegradeRetry returns true if any failure has been retried >= degradeThreshold times.
func shouldDegradeRetry(failures []FailureContext) bool {
	for _, f := range failures {
		if f.AttemptCount >= degradeThreshold {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run "TestBuildFailureContexts|TestFormatFailureContext|TestShouldDegradeRetry" ./internal/agent/ -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/failure_context.go internal/agent/failure_context_test.go
git commit -m "feat(agent): failure context builder with prompt formatting and degradation detection"
```

---

### Task 6: Wire Failure Context into REFLECT (A3 — integration)

**Files:**
- Modify: `internal/agent/reflect.go:271-309`
- Modify: `internal/agent/cognitive_prompts.go:186-200`
- Modify: `internal/agent/cognitive.go` (replan loop ~lines 264–411)

- [ ] **Step 1: Add `{{FAILURE_CONTEXT}}` placeholder to `ReflectUserPromptTemplate`**

In `internal/agent/cognitive_prompts.go`, change `ReflectUserPromptTemplate` (line 186–200) to:

```go
const ReflectUserPromptTemplate = `ORIGINAL GOAL:
{{GOAL}}

PLAN SUMMARY:
{{PLAN_SUMMARY}}

EXECUTION OBSERVATIONS:
{{OBSERVATIONS}}

STATISTICS:
{{STATS}}

{{FAILURE_CONTEXT}}
Score each dimension (completeness, accuracy, efficiency, relevance) 0–25 with explanations, then derive overall_confidence = sum / 100. Produce the JSON reflection now.`
```

- [ ] **Step 2: Update `buildReflectUserMessage` to inject failure context**

In `internal/agent/reflect.go`, modify `buildReflectUserMessage` (line 271). Change the function signature and add failure context substitution:

```go
func buildReflectUserMessage(state *CognitiveState, plan *TaskPlan, obsResult *ObservationResult, replanAttempt int) string {
	var obsSB strings.Builder
	if len(obsResult.Observations) == 0 {
		obsSB.WriteString("(no tool executions)")
	} else {
		for _, obs := range obsResult.Observations {
			_, _ = fmt.Fprintf(&obsSB, "- SubTask %s [%s]:\n", obs.SubTaskID, obs.ToolName)
			if obs.Denied {
				obsSB.WriteString("  Status: DENIED\n")
			} else if obs.Error != "" {
				_, _ = fmt.Fprintf(&obsSB, "  Status: FAILED\n  Error: %s\n", obs.Error)
			} else {
				output := obs.Output
				if len(output) > 1500 {
					output = output[:1500] + "...[truncated]"
				}
				_, _ = fmt.Fprintf(&obsSB, "  Status: SUCCESS\n  Output: %s\n", output)
			}
		}
	}

	stats := fmt.Sprintf(
		"Success: %d, Failures: %d, Denied: %d, Progress: %.0f%%, Error patterns: %s",
		obsResult.SuccessCount,
		obsResult.FailureCount,
		obsResult.DeniedCount,
		obsResult.OverallProgress*100,
		strings.Join(obsResult.ErrorPatterns, ", "),
	)

	enriched := enrichFailureContexts(obsResult.Failures, replanAttempt)
	failureCtx := formatFailureContextForPrompt(enriched)

	msg := ReflectUserPromptTemplate
	msg = strings.ReplaceAll(msg, "{{GOAL}}", state.Goal.Raw)
	msg = strings.ReplaceAll(msg, "{{PLAN_SUMMARY}}", plan.Summary)
	msg = strings.ReplaceAll(msg, "{{OBSERVATIONS}}", obsSB.String())
	msg = strings.ReplaceAll(msg, "{{STATS}}", stats)
	msg = strings.ReplaceAll(msg, "{{FAILURE_CONTEXT}}", failureCtx)
	return msg
}
```

- [ ] **Step 3: Update all callers of `buildReflectUserMessage`**

In `internal/agent/reflect.go`, the `Run` method calls `buildReflectUserMessage`. Find the call (around line 86) and add `replanAttempt` parameter. The `Run` method signature needs a new parameter:

Change `Reflector.Run` signature from:
```go
func (r *Reflector) Run(ctx context.Context, ch channel.Channel, target channel.MessageTarget, state *CognitiveState, plan *TaskPlan, obsResult *ObservationResult) (*Reflection, error) {
```
to:
```go
func (r *Reflector) Run(ctx context.Context, ch channel.Channel, target channel.MessageTarget, state *CognitiveState, plan *TaskPlan, obsResult *ObservationResult, replanAttempt int) (*Reflection, error) {
```

Update the `buildReflectUserMessage` call inside `Run` to pass `replanAttempt`.

In `internal/agent/cognitive.go`, update the call to `ca.reflector.Run(...)` (around line 352) to pass the current `attempt` variable as the last argument.

- [ ] **Step 4: Build to verify compilation**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: clean build

- [ ] **Step 5: Run existing reflect tests**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestParse ./internal/agent/ -v`
Expected: all PASS (parse tests don't call Run)

- [ ] **Step 6: Commit**

```bash
git add internal/agent/reflect.go internal/agent/cognitive_prompts.go internal/agent/cognitive.go
git commit -m "feat(agent): wire failure context into REFLECT phase with structured replan prompt"
```

---

### Task 7: Project Context Scanner (C1)

**Files:**
- Create: `internal/agent/project_scanner.go`
- Create: `internal/agent/project_scanner_test.go`
- Modify: `internal/agent/cognitive_types.go`

- [ ] **Step 1: Add `ProjectContext` type to `cognitive_types.go`**

Add after the `CognitiveState` struct:

```go
// ProjectContext holds auto-detected information about the current working directory.
type ProjectContext struct {
	Name           string   `json:"name"`
	Language       string   `json:"language"`
	BuildCommands  []string `json:"build_commands,omitempty"`
	KeyDirectories []string `json:"key_directories,omitempty"`
	HasReadme      bool     `json:"has_readme"`
	RawContent     string   `json:"-"` // formatted string for prompt injection
}
```

Add a `ProjectContext *ProjectContext` field to `CognitiveState`:

```go
type CognitiveState struct {
	// ... existing fields ...
	MaxTokensOverride int
	ProjectCtx        *ProjectContext // auto-detected project context (nil if not available)
}
```

- [ ] **Step 2: Write the failing test for project scanning**

Create `internal/agent/project_scanner_test.go`:

```go
package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectContextScanner_GoProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:\n\tgo build ./...\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test Project\n"), 0644); err != nil {
		t.Fatal(err)
	}

	scanner := NewProjectContextScanner()
	ctx := scanner.Scan(dir)

	if ctx == nil {
		t.Fatal("expected non-nil project context")
	}
	if ctx.Language != "go" {
		t.Errorf("Language = %q, want %q", ctx.Language, "go")
	}
	if ctx.Name != "example.com/test" {
		t.Errorf("Name = %q, want %q", ctx.Name, "example.com/test")
	}
	if !ctx.HasReadme {
		t.Error("expected HasReadme = true")
	}
	if ctx.RawContent == "" {
		t.Error("expected non-empty RawContent")
	}
}

func TestProjectContextScanner_NodeProject(t *testing.T) {
	dir := t.TempDir()
	pkg := `{"name":"my-app","scripts":{"build":"npm run build","test":"npm test"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatal(err)
	}

	scanner := NewProjectContextScanner()
	ctx := scanner.Scan(dir)

	if ctx == nil {
		t.Fatal("expected non-nil project context")
	}
	if ctx.Language != "javascript" {
		t.Errorf("Language = %q, want %q", ctx.Language, "javascript")
	}
	if ctx.Name != "my-app" {
		t.Errorf("Name = %q, want %q", ctx.Name, "my-app")
	}
}

func TestProjectContextScanner_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	scanner := NewProjectContextScanner()
	ctx := scanner.Scan(dir)

	if ctx != nil {
		t.Errorf("expected nil for empty directory, got %+v", ctx)
	}
}

func TestProjectContextScanner_Caching(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}

	scanner := NewProjectContextScanner()
	ctx1 := scanner.Scan(dir)
	ctx2 := scanner.Scan(dir)

	if ctx1 != ctx2 {
		t.Error("expected cached result to return same pointer")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestProjectContextScanner ./internal/agent/ -v`
Expected: FAIL — `NewProjectContextScanner` undefined

- [ ] **Step 4: Implement the project scanner**

Create `internal/agent/project_scanner.go`:

```go
package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type ProjectContextScanner struct {
	mu    sync.Mutex
	cache map[string]*ProjectContext
}

func NewProjectContextScanner() *ProjectContextScanner {
	return &ProjectContextScanner{cache: make(map[string]*ProjectContext)}
}

func (s *ProjectContextScanner) Scan(dir string) *ProjectContext {
	s.mu.Lock()
	if cached, ok := s.cache[dir]; ok {
		s.mu.Unlock()
		return cached
	}
	s.mu.Unlock()

	ctx := s.scan(dir)

	s.mu.Lock()
	s.cache[dir] = ctx
	s.mu.Unlock()
	return ctx
}

func (s *ProjectContextScanner) Invalidate(dir string) {
	s.mu.Lock()
	delete(s.cache, dir)
	s.mu.Unlock()
}

type projectDetector struct {
	file     string
	language string
	extract  func(dir string, content []byte) (name string, buildCmds []string)
}

var detectors = []projectDetector{
	{"go.mod", "go", extractGoMod},
	{"Cargo.toml", "rust", extractCargoToml},
	{"package.json", "javascript", extractPackageJSON},
	{"pyproject.toml", "python", extractPyproject},
	{"Makefile", "", extractMakefile},
}

func (s *ProjectContextScanner) scan(dir string) *ProjectContext {
	var pc ProjectContext
	detected := false

	for _, d := range detectors {
		path := filepath.Join(dir, d.file)
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		detected = true
		if d.language != "" && pc.Language == "" {
			pc.Language = d.language
		}
		if d.extract != nil {
			name, cmds := d.extract(dir, content)
			if name != "" && pc.Name == "" {
				pc.Name = name
			}
			pc.BuildCommands = append(pc.BuildCommands, cmds...)
		}
	}

	if !detected {
		return nil
	}

	_, err := os.Stat(filepath.Join(dir, "README.md"))
	pc.HasReadme = err == nil

	pc.KeyDirectories = scanKeyDirectories(dir)
	pc.RawContent = formatProjectContext(&pc)
	return &pc
}

func extractGoMod(_ string, content []byte) (string, []string) {
	lines := strings.Split(string(content), "\n")
	var name string
	for _, line := range lines {
		if strings.HasPrefix(line, "module ") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "module "))
			break
		}
	}
	return name, []string{"go build ./...", "go test ./..."}
}

func extractPackageJSON(_ string, content []byte) (string, []string) {
	var pkg struct {
		Name    string            `json:"name"`
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(content, &pkg); err != nil {
		return "", nil
	}
	var cmds []string
	for key, val := range pkg.Scripts {
		if key == "build" || key == "test" || key == "dev" {
			cmds = append(cmds, fmt.Sprintf("npm run %s → %s", key, val))
		}
	}
	return pkg.Name, cmds
}

func extractCargoToml(_ string, content []byte) (string, []string) {
	lines := strings.Split(string(content), "\n")
	var name string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "name") && strings.Contains(trimmed, "=") {
			parts := strings.SplitN(trimmed, "=", 2)
			name = strings.Trim(strings.TrimSpace(parts[1]), "\"")
			break
		}
	}
	return name, []string{"cargo build", "cargo test"}
}

func extractPyproject(_ string, content []byte) (string, []string) {
	lines := strings.Split(string(content), "\n")
	var name string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "name") && strings.Contains(trimmed, "=") {
			parts := strings.SplitN(trimmed, "=", 2)
			name = strings.Trim(strings.TrimSpace(parts[1]), "\"")
			break
		}
	}
	return name, []string{"python -m pytest"}
}

func extractMakefile(_ string, content []byte) (string, []string) {
	lines := strings.Split(string(content), "\n")
	var cmds []string
	for _, line := range lines {
		if len(line) > 0 && line[0] != '\t' && line[0] != '#' && strings.Contains(line, ":") {
			target := strings.TrimSuffix(strings.SplitN(line, ":", 2)[0], " ")
			if target == "build" || target == "test" || target == "lint" || target == "run" {
				cmds = append(cmds, fmt.Sprintf("make %s", target))
			}
		}
	}
	return "", cmds
}

func scanKeyDirectories(dir string) []string {
	candidates := []string{"cmd", "src", "internal", "pkg", "lib", "app", "test", "tests"}
	var found []string
	for _, c := range candidates {
		info, err := os.Stat(filepath.Join(dir, c))
		if err == nil && info.IsDir() {
			found = append(found, c+"/")
		}
	}
	return found
}

func formatProjectContext(pc *ProjectContext) string {
	var sb strings.Builder
	if pc.Name != "" {
		fmt.Fprintf(&sb, "Project: %s\n", pc.Name)
	}
	if pc.Language != "" {
		fmt.Fprintf(&sb, "Language: %s\n", pc.Language)
	}
	if len(pc.BuildCommands) > 0 {
		sb.WriteString("Build commands:\n")
		for _, cmd := range pc.BuildCommands {
			fmt.Fprintf(&sb, "  - %s\n", cmd)
		}
	}
	if len(pc.KeyDirectories) > 0 {
		fmt.Fprintf(&sb, "Key directories: %s\n", strings.Join(pc.KeyDirectories, ", "))
	}
	return sb.String()
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestProjectContextScanner ./internal/agent/ -v`
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/agent/project_scanner.go internal/agent/project_scanner_test.go internal/agent/cognitive_types.go
git commit -m "feat(agent): project context scanner with caching and multi-language detection"
```

---

### Task 8: Wire Project Context into PLAN (C1 — integration)

**Files:**
- Modify: `internal/agent/cognitive_prompts.go:40-62`
- Modify: `internal/agent/plan.go:121-206`
- Modify: `internal/agent/perceive.go`
- Modify: `internal/agent/cognitive.go`

- [ ] **Step 1: Add `{{PROJECT_CONTEXT}}` placeholder to `PlanUserPromptTemplate`**

In `internal/agent/cognitive_prompts.go`, modify `PlanUserPromptTemplate` (lines 42–62). Insert the new section between KNOWLEDGE GRAPH and RECENT CONVERSATION:

```go
const PlanUserPromptTemplate = `USER REQUEST:
{{USER_REQUEST}}

AVAILABLE TOOLS:
{{TOOLS}}

RELEVANT MEMORIES:
{{MEMORIES}}

KNOWLEDGE BASE:
{{KNOWLEDGE}}

KNOWLEDGE GRAPH:
{{GRAPH}}

PROJECT CONTEXT:
{{PROJECT_CONTEXT}}

RECENT CONVERSATION:
{{HISTORY}}

{{PREFERENCES}}
{{STRATEGY}}
Produce the JSON execution plan now.`
```

- [ ] **Step 2: Add project context substitution in `buildPlanUserMessage`**

In `internal/agent/plan.go`, after the graph section replacement (line 187) and before the preferences replacement (line 190), add:

```go
	// Project context section
	projectCtx := "(none)"
	if state.ProjectCtx != nil && state.ProjectCtx.RawContent != "" {
		projectCtx = state.ProjectCtx.RawContent
	}
	msg = strings.ReplaceAll(msg, "{{PROJECT_CONTEXT}}", projectCtx)
```

- [ ] **Step 3: Wire `ProjectContextScanner` into `Perceiver`**

In `internal/agent/perceive.go`, add a `scanner *ProjectContextScanner` field to the `Perceiver` struct and a setter:

```go
type Perceiver struct {
	memStore memory.Store
	searcher knowledge.Searcher
	graph    graph.Graph
	rlPolicy RLPolicy
	scanner  *ProjectContextScanner
}

func (p *Perceiver) SetProjectScanner(s *ProjectContextScanner) {
	p.scanner = s
}
```

In `Perceiver.Run`, after building the base `CognitiveState` (around line 110), add project scanning:

```go
	if p.scanner != nil {
		cwd, _ := os.Getwd()
		state.ProjectCtx = p.scanner.Scan(cwd)
	}
```

Add `"os"` to the imports in `perceive.go`.

- [ ] **Step 4: Wire scanner construction in `cognitive.go`**

In `internal/agent/cognitive.go`, inside `NewCognitiveAgent`, create and inject the scanner:

```go
	scanner := NewProjectContextScanner()
	ca.perceiver.SetProjectScanner(scanner)
```

- [ ] **Step 5: Build and verify**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: clean build

- [ ] **Step 6: Run full test suite**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/agent/ -v -count=1`
Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add internal/agent/cognitive_prompts.go internal/agent/plan.go internal/agent/perceive.go internal/agent/cognitive.go
git commit -m "feat(agent): wire project context scanner into PERCEIVE→PLAN pipeline"
```

---

## Phase 3: Browser Tools + Cache + Git

### Task 9: Browser Search Tool (B2)

**Files:**
- Create: `internal/tool/browser_search.go`
- Create: `internal/tool/browser_search_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/tool/browser_search_test.go`:

```go
package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBrowserSearchTool_Name(t *testing.T) {
	bst := NewBrowserSearchTool(10*time.Second, false)
	if bst.Name() != "browser_search" {
		t.Errorf("Name() = %q, want %q", bst.Name(), "browser_search")
	}
}

func TestBrowserSearchTool_IsReadOnly(t *testing.T) {
	bst := NewBrowserSearchTool(10*time.Second, false)
	if !bst.IsReadOnly() {
		t.Error("expected IsReadOnly() = true")
	}
}

func TestBrowserSearchTool_Execute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>
			<div class="result"><h3><a href="https://example.com/1">Result One</a></h3><p>First snippet</p></div>
			<div class="result"><h3><a href="https://example.com/2">Result Two</a></h3><p>Second snippet</p></div>
		</body></html>`))
	}))
	defer server.Close()

	bst := NewBrowserSearchTool(10*time.Second, false)
	bst.searchURL = server.URL + "?q=%s"

	input, _ := json.Marshal(map[string]string{"query": "test search"})
	result, err := bst.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}

	var results []searchResultItem
	if err := json.Unmarshal([]byte(result.Output), &results); err != nil {
		t.Fatalf("output is not valid JSON array: %v\nraw: %s", err, result.Output)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one search result")
	}
}

func TestBrowserSearchTool_EmptyQuery(t *testing.T) {
	bst := NewBrowserSearchTool(10*time.Second, false)
	input, _ := json.Marshal(map[string]string{"query": ""})
	result, err := bst.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected error for empty query")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestBrowserSearchTool ./internal/tool/ -v`
Expected: FAIL — `NewBrowserSearchTool` undefined

- [ ] **Step 3: Implement the browser search tool**

Create `internal/tool/browser_search.go`:

```go
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type BrowserSearchTool struct {
	client    *http.Client
	approval  bool
	searchURL string
}

type searchInput struct {
	Query string `json:"query"`
	Page  int    `json:"page,omitempty"`
}

type searchResultItem struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func NewBrowserSearchTool(timeout time.Duration, requiresApproval bool) *BrowserSearchTool {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &BrowserSearchTool{
		client:    &http.Client{Timeout: timeout},
		approval:  requiresApproval,
		searchURL: "https://html.duckduckgo.com/html/?q=%s",
	}
}

func (b *BrowserSearchTool) Name() string             { return "browser_search" }
func (b *BrowserSearchTool) Description() string       { return "Search the web and return structured results: [{title, url, snippet}]." }
func (b *BrowserSearchTool) RequiresApproval() bool    { return b.approval }
func (b *BrowserSearchTool) IsReadOnly() bool          { return true }

func (b *BrowserSearchTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		IsReadOnly:      true,
		IsDestructive:   false,
		RequiresNetwork: true,
		ApprovalMode:    "auto",
	}
}

func (b *BrowserSearchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "The search query",
			},
			"page": map[string]any{
				"type":        "integer",
				"description": "Page number for pagination (default: 1)",
			},
		},
		"required": []string{"query"},
	}
}

func (b *BrowserSearchTool) Execute(ctx context.Context, input []byte) (Result, error) {
	var in searchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: "invalid input: " + err.Error()}, nil
	}
	if in.Query == "" {
		return Result{Error: "query is required"}, nil
	}

	searchURL := fmt.Sprintf(b.searchURL, url.QueryEscape(in.Query))

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return Result{Error: "failed to build request: " + err.Error()}, nil
	}
	req.Header.Set("User-Agent", "IronClaw/1.0")

	resp, err := b.client.Do(req)
	if err != nil {
		return Result{Error: "search request failed: " + err.Error()}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOutputSize))
	if err != nil {
		return Result{Error: "failed to read response: " + err.Error()}, nil
	}

	results := parseSearchResults(string(body))

	resultJSON, _ := json.Marshal(results)
	return Result{
		Output: string(resultJSON),
		Metadata: map[string]any{
			"result_count": len(results),
			"query":        in.Query,
		},
	}, nil
}

var (
	resultBlockRe = regexp.MustCompile(`(?si)<a[^>]+class="result__a"[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	snippetRe     = regexp.MustCompile(`(?si)<a[^>]+class="result__snippet"[^>]*>(.*?)</a>`)
	htmlTagRe     = regexp.MustCompile(`<[^>]+>`)
)

func parseSearchResults(html string) []searchResultItem {
	links := resultBlockRe.FindAllStringSubmatch(html, -1)
	snippets := snippetRe.FindAllStringSubmatch(html, -1)

	var results []searchResultItem
	for i, match := range links {
		if len(match) < 3 {
			continue
		}
		item := searchResultItem{
			URL:   cleanURL(match[1]),
			Title: stripHTML(match[2]),
		}
		if i < len(snippets) && len(snippets[i]) >= 2 {
			item.Snippet = stripHTML(snippets[i][1])
		}
		if item.URL != "" && item.Title != "" {
			results = append(results, item)
		}
	}

	if len(results) == 0 {
		results = parseGenericResults(html)
	}
	return results
}

func parseGenericResults(html string) []searchResultItem {
	linkRe := regexp.MustCompile(`<a[^>]+href="(https?://[^"]+)"[^>]*>(.*?)</a>`)
	matches := linkRe.FindAllStringSubmatch(html, 20)

	var results []searchResultItem
	seen := make(map[string]bool)
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		href := m[1]
		title := stripHTML(m[2])
		if title == "" || seen[href] {
			continue
		}
		seen[href] = true
		results = append(results, searchResultItem{
			URL:   href,
			Title: title,
		})
	}
	return results
}

func stripHTML(s string) string {
	return strings.TrimSpace(htmlTagRe.ReplaceAllString(s, ""))
}

func cleanURL(raw string) string {
	if u, err := url.QueryUnescape(raw); err == nil {
		if strings.HasPrefix(u, "//duckduckgo.com/l/?uddg=") {
			if parsed, err := url.Parse(u); err == nil {
				return parsed.Query().Get("uddg")
			}
		}
		return u
	}
	return raw
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestBrowserSearchTool ./internal/tool/ -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tool/browser_search.go internal/tool/browser_search_test.go
git commit -m "feat(tool): add browser_search tool with structured result parsing"
```

---

### Task 10: Browser Extract Tool (B2)

**Files:**
- Create: `internal/tool/browser_extract.go`
- Create: `internal/tool/browser_extract_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/tool/browser_extract_test.go`:

```go
package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBrowserExtractTool_Name(t *testing.T) {
	bet := NewBrowserExtractTool(10*time.Second, false)
	if bet.Name() != "browser_extract" {
		t.Errorf("Name() = %q, want %q", bet.Name(), "browser_extract")
	}
}

func TestBrowserExtractTool_Execute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html><head><title>Test Article</title></head>
<body>
<nav>Navigation links here</nav>
<article>
<h1>Test Article</h1>
<p>This is the main content of the article. It contains important information.</p>
<p>Second paragraph with more details.</p>
</article>
<footer>Footer content</footer>
</body></html>`))
	}))
	defer server.Close()

	bet := NewBrowserExtractTool(10*time.Second, false)
	input, _ := json.Marshal(map[string]string{"url": server.URL})

	result, err := bet.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}

	if !strings.Contains(result.Output, "main content") {
		t.Errorf("expected output to contain article content, got: %s", result.Output)
	}
}

func TestBrowserExtractTool_InvalidURL(t *testing.T) {
	bet := NewBrowserExtractTool(10*time.Second, false)
	input, _ := json.Marshal(map[string]string{"url": "ftp://invalid"})

	result, err := bet.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected error for non-http URL")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestBrowserExtractTool ./internal/tool/ -v`
Expected: FAIL — `NewBrowserExtractTool` undefined

- [ ] **Step 3: Implement the browser extract tool**

Create `internal/tool/browser_extract.go`:

```go
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type BrowserExtractTool struct {
	client   *http.Client
	approval bool
}

type extractInput struct {
	URL  string `json:"url"`
	Page int    `json:"page,omitempty"`
}

func NewBrowserExtractTool(timeout time.Duration, requiresApproval bool) *BrowserExtractTool {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &BrowserExtractTool{
		client:   &http.Client{Timeout: timeout},
		approval: requiresApproval,
	}
}

func (b *BrowserExtractTool) Name() string             { return "browser_extract" }
func (b *BrowserExtractTool) Description() string       { return "Fetch a URL and extract the main content as clean Markdown, stripping navigation, ads, and boilerplate." }
func (b *BrowserExtractTool) RequiresApproval() bool    { return b.approval }
func (b *BrowserExtractTool) IsReadOnly() bool          { return true }

func (b *BrowserExtractTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		IsReadOnly:      true,
		IsDestructive:   false,
		RequiresNetwork: true,
		ApprovalMode:    "auto",
	}
}

func (b *BrowserExtractTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The URL to extract content from",
			},
			"page": map[string]any{
				"type":        "integer",
				"description": "Content page for paginated output (default: 1)",
			},
		},
		"required": []string{"url"},
	}
}

const extractPageSize = 4000

func (b *BrowserExtractTool) Execute(ctx context.Context, input []byte) (Result, error) {
	var in extractInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: "invalid input: " + err.Error()}, nil
	}
	if in.URL == "" {
		return Result{Error: "url is required"}, nil
	}

	parsed, err := url.Parse(in.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return Result{Error: "only http/https URLs are supported"}, nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", in.URL, nil)
	if err != nil {
		return Result{Error: "failed to build request: " + err.Error()}, nil
	}
	req.Header.Set("User-Agent", "IronClaw/1.0")

	resp, err := b.client.Do(req)
	if err != nil {
		return Result{Error: "request failed: " + err.Error()}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return Result{Error: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status)}, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxOutputSize)))
	if err != nil {
		return Result{Error: "failed to read response: " + err.Error()}, nil
	}

	markdown := htmlToReadableMarkdown(string(body))

	page := in.Page
	if page < 1 {
		page = 1
	}
	totalPages := (len(markdown) + extractPageSize - 1) / extractPageSize
	if totalPages == 0 {
		totalPages = 1
	}

	start := (page - 1) * extractPageSize
	if start >= len(markdown) {
		return Result{
			Output: "(no more content)",
			Metadata: map[string]any{
				"page":        page,
				"total_pages": totalPages,
				"url":         in.URL,
			},
		}, nil
	}

	end := start + extractPageSize
	if end > len(markdown) {
		end = len(markdown)
	}

	output := markdown[start:end]
	isPartial := end < len(markdown)

	return Result{
		Output:    output,
		IsPartial: isPartial,
		Metadata: map[string]any{
			"page":        page,
			"total_pages": totalPages,
			"url":         in.URL,
		},
	}, nil
}

var (
	scriptStyleRe = regexp.MustCompile(`(?si)<(script|style|nav|footer|header|aside)[^>]*>.*?</\1>`)
	commentRe     = regexp.MustCompile(`(?s)<!--.*?-->`)
	headingRe     = regexp.MustCompile(`(?i)<h([1-6])[^>]*>(.*?)</h[1-6]>`)
	paragraphRe   = regexp.MustCompile(`(?si)<p[^>]*>(.*?)</p>`)
	listItemRe    = regexp.MustCompile(`(?si)<li[^>]*>(.*?)</li>`)
	anchorRe      = regexp.MustCompile(`(?si)<a[^>]+href="([^"]*)"[^>]*>(.*?)</a>`)
	codeBlockRe   = regexp.MustCompile(`(?si)<(pre|code)[^>]*>(.*?)</\1>`)
	brRe          = regexp.MustCompile(`(?i)<br\s*/?>`)
	allTagsRe     = regexp.MustCompile(`<[^>]+>`)
	multiNewline  = regexp.MustCompile(`\n{3,}`)
)

func htmlToReadableMarkdown(html string) string {
	text := html
	text = commentRe.ReplaceAllString(text, "")
	text = scriptStyleRe.ReplaceAllString(text, "")

	text = codeBlockRe.ReplaceAllStringFunc(text, func(m string) string {
		matches := codeBlockRe.FindStringSubmatch(m)
		if len(matches) >= 3 {
			code := allTagsRe.ReplaceAllString(matches[2], "")
			return "\n```\n" + strings.TrimSpace(code) + "\n```\n"
		}
		return m
	})

	text = headingRe.ReplaceAllStringFunc(text, func(m string) string {
		matches := headingRe.FindStringSubmatch(m)
		if len(matches) >= 3 {
			level := matches[1][0] - '0'
			prefix := strings.Repeat("#", int(level))
			content := allTagsRe.ReplaceAllString(matches[2], "")
			return "\n" + prefix + " " + strings.TrimSpace(content) + "\n"
		}
		return m
	})

	text = anchorRe.ReplaceAllStringFunc(text, func(m string) string {
		matches := anchorRe.FindStringSubmatch(m)
		if len(matches) >= 3 {
			linkText := allTagsRe.ReplaceAllString(matches[2], "")
			linkText = strings.TrimSpace(linkText)
			if linkText == "" {
				return ""
			}
			return fmt.Sprintf("[%s](%s)", linkText, matches[1])
		}
		return m
	})

	text = paragraphRe.ReplaceAllStringFunc(text, func(m string) string {
		matches := paragraphRe.FindStringSubmatch(m)
		if len(matches) >= 2 {
			content := allTagsRe.ReplaceAllString(matches[1], "")
			return "\n" + strings.TrimSpace(content) + "\n"
		}
		return m
	})

	text = listItemRe.ReplaceAllStringFunc(text, func(m string) string {
		matches := listItemRe.FindStringSubmatch(m)
		if len(matches) >= 2 {
			content := allTagsRe.ReplaceAllString(matches[1], "")
			return "- " + strings.TrimSpace(content) + "\n"
		}
		return m
	})

	text = brRe.ReplaceAllString(text, "\n")
	text = allTagsRe.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = multiNewline.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestBrowserExtractTool ./internal/tool/ -v`
Expected: all PASS

- [ ] **Step 5: Register both browser tools in gateway**

In `internal/gateway/init_tools.go`, after the browser tool registration (around line 29), add:

```go
	if gw.cfg.Tools.Browser.Enabled {
		gw.tools.Register(tool.NewBrowserTool(gw.cfg.Tools.Browser.Timeout, gw.cfg.Tools.Browser.RequiresApproval))
		gw.tools.Register(tool.NewBrowserSearchTool(gw.cfg.Tools.Browser.Timeout, gw.cfg.Tools.Browser.RequiresApproval))
		gw.tools.Register(tool.NewBrowserExtractTool(gw.cfg.Tools.Browser.Timeout, gw.cfg.Tools.Browser.RequiresApproval))
	}
```

Remove the earlier standalone `gw.tools.Register(tool.NewBrowserTool(...))` line to avoid duplicate registration.

- [ ] **Step 6: Build and verify**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: clean build

- [ ] **Step 7: Commit**

```bash
git add internal/tool/browser_extract.go internal/tool/browser_extract_test.go internal/gateway/init_tools.go
git commit -m "feat(tool): add browser_extract tool with HTML→Markdown conversion and pagination"
```

---

### Task 11: Tool Result Cache (B3)

**Files:**
- Create: `internal/agent/tool_cache.go`
- Create: `internal/agent/tool_cache_test.go`
- Modify: `internal/agent/act.go`

- [ ] **Step 1: Write the failing test**

Create `internal/agent/tool_cache_test.go`:

```go
package agent

import (
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/tool"
)

func TestToolResultCache_HitAndMiss(t *testing.T) {
	cache := NewToolResultCache()

	result := tool.Result{Output: "hello"}
	cache.Put("file_read", `{"path":"a.txt"}`, result)

	hit, ok := cache.Get("file_read", `{"path":"a.txt"}`)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if hit.Output != "hello" {
		t.Errorf("Output = %q, want %q", hit.Output, "hello")
	}

	_, ok = cache.Get("file_read", `{"path":"b.txt"}`)
	if ok {
		t.Error("expected cache miss for different input")
	}
}

func TestToolResultCache_InvalidateByPath(t *testing.T) {
	cache := NewToolResultCache()

	cache.Put("file_read", `{"path":"a.txt"}`, tool.Result{Output: "v1"})
	cache.Put("file_read", `{"path":"b.txt"}`, tool.Result{Output: "v2"})

	cache.InvalidatePath("a.txt")

	_, ok := cache.Get("file_read", `{"path":"a.txt"}`)
	if ok {
		t.Error("expected cache miss after invalidation")
	}

	_, ok = cache.Get("file_read", `{"path":"b.txt"}`)
	if !ok {
		t.Error("expected cache hit for non-invalidated path")
	}
}

func TestToolResultCache_Clear(t *testing.T) {
	cache := NewToolResultCache()
	cache.Put("file_read", `{"path":"a.txt"}`, tool.Result{Output: "v1"})

	cache.Clear()

	_, ok := cache.Get("file_read", `{"path":"a.txt"}`)
	if ok {
		t.Error("expected cache miss after clear")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestToolResultCache ./internal/agent/ -v`
Expected: FAIL — `NewToolResultCache` undefined

- [ ] **Step 3: Implement the tool result cache**

Create `internal/agent/tool_cache.go`:

```go
package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Forest-Isle/IronClaw/internal/tool"
)

type cacheEntry struct {
	result tool.Result
	paths  []string
}

type ToolResultCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
}

func NewToolResultCache() *ToolResultCache {
	return &ToolResultCache{entries: make(map[string]*cacheEntry)}
}

func cacheKey(toolName, input string) string {
	h := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%s:%x", toolName, h)
}

func (c *ToolResultCache) Get(toolName, input string) (tool.Result, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := cacheKey(toolName, input)
	entry, ok := c.entries[key]
	if !ok {
		return tool.Result{}, false
	}
	return entry.result, true
}

func (c *ToolResultCache) Put(toolName, input string, result tool.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := cacheKey(toolName, input)
	paths := extractPathsFromInput(input)
	c.entries[key] = &cacheEntry{result: result, paths: paths}
}

func (c *ToolResultCache) InvalidatePath(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, entry := range c.entries {
		for _, p := range entry.paths {
			if p == path || strings.HasPrefix(p, path+"/") {
				delete(c.entries, key)
				break
			}
		}
	}
}

func (c *ToolResultCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*cacheEntry)
}

func extractPathsFromInput(input string) []string {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(input), &parsed); err != nil {
		return nil
	}

	var paths []string
	for _, key := range []string{"path", "file_path", "directory"} {
		if v, ok := parsed[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				paths = append(paths, s)
			}
		}
	}
	return paths
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestToolResultCache ./internal/agent/ -v`
Expected: all PASS

- [ ] **Step 5: Wire cache into Executor**

In `internal/agent/act.go`, add a `cache *ToolResultCache` field to `Executor`:

```go
type Executor struct {
	tools        *tool.Registry
	db           *store.DB
	approvalFunc ApprovalFunc
	cfg          config.CognitiveConfig
	rlPolicy     RLPolicy
	hookMgr      *hook.Manager
	permEngine   *tool.PermissionEngine
	cache        *ToolResultCache
}
```

Add a setter:

```go
func (e *Executor) SetToolCache(cache *ToolResultCache) {
	e.cache = cache
}
```

In `executeSubTask` (around line 162), after resolving the tool and before executing it, add cache lookup for read-only tools:

```go
	if e.cache != nil && tool.IsToolReadOnly(t) {
		if cached, ok := e.cache.Get(st.ToolName, st.ToolInput); ok {
			obs.Output = cached.Output
			st.Status = SubTaskDone
			obs.Output += "\n(cached)"
			return obs
		}
	}
```

After successful execution, cache the result for read-only tools:

```go
	if e.cache != nil && tool.IsToolReadOnly(t) {
		e.cache.Put(st.ToolName, st.ToolInput, result)
	}
```

For write tools, invalidate cache entries for affected paths:

```go
	if e.cache != nil && !tool.IsToolReadOnly(t) {
		if pst, ok := t.(tool.PathScopedTool); ok {
			if paths, err := pst.ExtractPaths([]byte(st.ToolInput)); err == nil {
				for _, p := range paths {
					e.cache.InvalidatePath(p)
				}
			}
		}
	}
```

In `NewCognitiveAgent` (`cognitive.go`), wire the cache:

```go
	cache := NewToolResultCache()
	ca.executor.SetToolCache(cache)
```

- [ ] **Step 6: Build and verify**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: clean build

- [ ] **Step 7: Commit**

```bash
git add internal/agent/tool_cache.go internal/agent/tool_cache_test.go internal/agent/act.go internal/agent/cognitive.go
git commit -m "feat(agent): tool result cache with automatic invalidation on writes"
```

---

### Task 12: Git State Awareness (C2)

**Files:**
- Create: `internal/agent/git_context.go`
- Create: `internal/agent/git_context_test.go`
- Modify: `internal/agent/cognitive_types.go`
- Modify: `internal/agent/perceive.go`
- Modify: `internal/agent/cognitive_prompts.go`
- Modify: `internal/agent/plan.go`

- [ ] **Step 1: Add `GitState` type to `cognitive_types.go`**

Add to `internal/agent/cognitive_types.go`:

```go
// GitState holds the current git repository state for context injection.
type GitState struct {
	Branch           string   `json:"branch"`
	UncommittedFiles []string `json:"uncommitted_files,omitempty"`
	RecentCommits    []string `json:"recent_commits,omitempty"`
	RawContent       string   `json:"-"` // formatted string for prompt injection
}
```

Add a `GitState *GitState` field to `CognitiveState`:

```go
type CognitiveState struct {
	// ... existing fields ...
	ProjectCtx        *ProjectContext
	GitState          *GitState // current git state (nil if not in a git repo)
}
```

- [ ] **Step 2: Write the failing test**

Create `internal/agent/git_context_test.go`:

```go
package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitContextProvider_InGitRepo(t *testing.T) {
	dir := t.TempDir()

	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cmd %v failed: %v\n%s", args, err, out)
		}
	}

	testFile := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("git", "add", ".")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	cmd = exec.Command("git", "commit", "-m", "initial commit")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}

	provider := NewGitContextProvider()
	state := provider.Collect(dir)

	if state == nil {
		t.Fatal("expected non-nil git state")
	}
	if state.Branch == "" {
		t.Error("expected non-empty branch")
	}
	if len(state.UncommittedFiles) == 0 {
		t.Error("expected uncommitted files")
	}
	if len(state.RecentCommits) == 0 {
		t.Error("expected at least one recent commit")
	}
	if state.RawContent == "" {
		t.Error("expected non-empty RawContent")
	}
}

func TestGitContextProvider_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	provider := NewGitContextProvider()
	state := provider.Collect(dir)

	if state != nil {
		t.Errorf("expected nil for non-git directory, got %+v", state)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestGitContextProvider ./internal/agent/ -v`
Expected: FAIL — `NewGitContextProvider` undefined

- [ ] **Step 4: Implement the git context provider**

Create `internal/agent/git_context.go`:

```go
package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type GitContextProvider struct {
	timeout time.Duration
}

func NewGitContextProvider() *GitContextProvider {
	return &GitContextProvider{timeout: 5 * time.Second}
}

func (g *GitContextProvider) Collect(dir string) *GitState {
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()

	if _, err := g.runGit(ctx, dir, "rev-parse", "--git-dir"); err != nil {
		return nil
	}

	state := &GitState{}

	if branch, err := g.runGit(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		state.Branch = strings.TrimSpace(branch)
	}

	if status, err := g.runGit(ctx, dir, "status", "--short"); err == nil {
		lines := strings.Split(strings.TrimSpace(status), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				state.UncommittedFiles = append(state.UncommittedFiles, line)
			}
		}
	}

	if log, err := g.runGit(ctx, dir, "log", "--oneline", "-5"); err == nil {
		lines := strings.Split(strings.TrimSpace(log), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				state.RecentCommits = append(state.RecentCommits, line)
			}
		}
	}

	state.RawContent = formatGitState(state)
	return state
}

func (g *GitContextProvider) runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

func formatGitState(s *GitState) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Branch: %s\n", s.Branch)

	if len(s.UncommittedFiles) > 0 {
		sb.WriteString("Uncommitted changes:\n")
		for _, f := range s.UncommittedFiles {
			fmt.Fprintf(&sb, "  %s\n", f)
		}
	} else {
		sb.WriteString("Working tree clean\n")
	}

	if len(s.RecentCommits) > 0 {
		sb.WriteString("Recent commits:\n")
		for _, c := range s.RecentCommits {
			fmt.Fprintf(&sb, "  %s\n", c)
		}
	}
	return sb.String()
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestGitContextProvider ./internal/agent/ -v`
Expected: all PASS

- [ ] **Step 6: Wire git state into PERCEIVE→PLAN**

Add `{{GIT_STATE}}` placeholder to `PlanUserPromptTemplate` in `cognitive_prompts.go`, after `PROJECT CONTEXT`:

```
PROJECT CONTEXT:
{{PROJECT_CONTEXT}}

GIT STATE:
{{GIT_STATE}}
```

In `plan.go` `buildPlanUserMessage`, after the project context substitution, add:

```go
	// Git state section
	gitState := "(none)"
	if state.GitState != nil && state.GitState.RawContent != "" {
		gitState = state.GitState.RawContent
	}
	msg = strings.ReplaceAll(msg, "{{GIT_STATE}}", gitState)
```

In `perceive.go`, add a `gitProvider *GitContextProvider` field to `Perceiver` and a setter:

```go
func (p *Perceiver) SetGitProvider(g *GitContextProvider) {
	p.gitProvider = g
}
```

In `Perceiver.Run`, after the project scanner call, add:

```go
	if p.gitProvider != nil {
		cwd, _ := os.Getwd()
		state.GitState = p.gitProvider.Collect(cwd)
	}
```

In `cognitive.go` `NewCognitiveAgent`, wire the git provider:

```go
	gitProvider := NewGitContextProvider()
	ca.perceiver.SetGitProvider(gitProvider)
```

- [ ] **Step 7: Build and verify**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: clean build

- [ ] **Step 8: Commit**

```bash
git add internal/agent/git_context.go internal/agent/git_context_test.go internal/agent/cognitive_types.go internal/agent/perceive.go internal/agent/cognitive_prompts.go internal/agent/plan.go internal/agent/cognitive.go
git commit -m "feat(agent): git state awareness with branch/uncommitted/commits injection into PLAN"
```

---

## Phase 4: Checkpoints + Dynamic Budget

### Task 13: Task Checkpoint Store (A1)

**Files:**
- Create: `internal/store/migrations/018_task_checkpoints.sql`
- Create: `internal/agent/checkpoint.go`
- Create: `internal/agent/checkpoint_test.go`

- [ ] **Step 1: Create the migration file**

Create `internal/store/migrations/018_task_checkpoints.sql`:

```sql
CREATE TABLE IF NOT EXISTS task_checkpoints (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    subtask_index INTEGER NOT NULL DEFAULT 0,
    observations_json TEXT NOT NULL DEFAULT '[]',
    plan_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(session_id)
);

CREATE INDEX IF NOT EXISTS idx_task_checkpoints_session ON task_checkpoints(session_id);
```

- [ ] **Step 2: Write the failing test for `CheckpointStore`**

Create `internal/agent/checkpoint_test.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

func setupTestDB(t *testing.T) *store.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbPath)
	})
	return db
}

func TestSQLiteCheckpointStore_SaveAndLoad(t *testing.T) {
	db := setupTestDB(t)
	cs := NewSQLiteCheckpointStore(db)

	plan := &TaskPlan{
		Summary:  "test plan",
		SubTasks: []*SubTask{{ID: "1", Description: "do thing", ToolName: "bash"}},
	}
	planJSON, _ := json.Marshal(plan)

	observations := []Observation{
		{SubTaskID: "1", ToolName: "bash", Output: "hello"},
	}
	obsJSON, _ := json.Marshal(observations)

	cp := &TaskCheckpoint{
		ID:               "cp-1",
		SessionID:        "sess-123",
		SubTaskIndex:     1,
		ObservationsJSON: string(obsJSON),
		PlanJSON:         string(planJSON),
	}

	ctx := context.Background()
	if err := cs.Save(ctx, cp); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := cs.Load(ctx, "sess-123")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil checkpoint")
	}
	if loaded.SubTaskIndex != 1 {
		t.Errorf("SubTaskIndex = %d, want 1", loaded.SubTaskIndex)
	}
	if loaded.SessionID != "sess-123" {
		t.Errorf("SessionID = %q, want %q", loaded.SessionID, "sess-123")
	}
}

func TestSQLiteCheckpointStore_Delete(t *testing.T) {
	db := setupTestDB(t)
	cs := NewSQLiteCheckpointStore(db)

	cp := &TaskCheckpoint{
		ID:               "cp-2",
		SessionID:        "sess-456",
		SubTaskIndex:     0,
		ObservationsJSON: "[]",
		PlanJSON:         "{}",
	}

	ctx := context.Background()
	_ = cs.Save(ctx, cp)

	if err := cs.Delete(ctx, "sess-456"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	loaded, err := cs.Load(ctx, "sess-456")
	if err != nil {
		t.Fatalf("Load after delete failed: %v", err)
	}
	if loaded != nil {
		t.Error("expected nil after delete")
	}
}

func TestSQLiteCheckpointStore_LoadNonexistent(t *testing.T) {
	db := setupTestDB(t)
	cs := NewSQLiteCheckpointStore(db)

	loaded, err := cs.Load(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("Load should not error for missing: %v", err)
	}
	if loaded != nil {
		t.Error("expected nil for nonexistent session")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestSQLiteCheckpointStore ./internal/agent/ -v`
Expected: FAIL — `NewSQLiteCheckpointStore` undefined

- [ ] **Step 4: Implement the checkpoint store**

Create `internal/agent/checkpoint.go`:

```go
package agent

import (
	"context"
	"database/sql"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

// TaskCheckpoint represents a saved execution state for resume.
type TaskCheckpoint struct {
	ID               string
	SessionID        string
	SubTaskIndex     int
	ObservationsJSON string
	PlanJSON         string
	CreatedAt        string
}

// CheckpointStore persists and retrieves task checkpoints.
type CheckpointStore interface {
	Save(ctx context.Context, cp *TaskCheckpoint) error
	Load(ctx context.Context, sessionID string) (*TaskCheckpoint, error)
	Delete(ctx context.Context, sessionID string) error
}

// SQLiteCheckpointStore implements CheckpointStore using the shared SQLite DB.
type SQLiteCheckpointStore struct {
	db *store.DB
}

func NewSQLiteCheckpointStore(db *store.DB) *SQLiteCheckpointStore {
	return &SQLiteCheckpointStore{db: db}
}

func (s *SQLiteCheckpointStore) Save(ctx context.Context, cp *TaskCheckpoint) error {
	query := `INSERT OR REPLACE INTO task_checkpoints
		(id, session_id, subtask_index, observations_json, plan_json)
		VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query,
		cp.ID, cp.SessionID, cp.SubTaskIndex, cp.ObservationsJSON, cp.PlanJSON)
	return err
}

func (s *SQLiteCheckpointStore) Load(ctx context.Context, sessionID string) (*TaskCheckpoint, error) {
	query := `SELECT id, session_id, subtask_index, observations_json, plan_json, created_at
		FROM task_checkpoints WHERE session_id = ?`

	var cp TaskCheckpoint
	err := s.db.QueryRowContext(ctx, query, sessionID).Scan(
		&cp.ID, &cp.SessionID, &cp.SubTaskIndex,
		&cp.ObservationsJSON, &cp.PlanJSON, &cp.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cp, nil
}

func (s *SQLiteCheckpointStore) Delete(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM task_checkpoints WHERE session_id = ?", sessionID)
	return err
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestSQLiteCheckpointStore ./internal/agent/ -v`
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store/migrations/018_task_checkpoints.sql internal/agent/checkpoint.go internal/agent/checkpoint_test.go
git commit -m "feat(agent): checkpoint store with SQLite persistence for task resume"
```

---

### Task 14: Wire Checkpoint Save/Resume into Cognitive Agent (A1 — integration)

**Files:**
- Modify: `internal/agent/cognitive.go`

- [ ] **Step 1: Add checkpoint store field to `CognitiveAgent`**

In `internal/agent/cognitive.go`, add to the `CognitiveAgent` struct:

```go
	checkpointStore CheckpointStore
```

Add a setter:

```go
func (ca *CognitiveAgent) SetCheckpointStore(cs CheckpointStore) {
	ca.checkpointStore = cs
}
```

- [ ] **Step 2: Add checkpoint save after each plan execution**

In `HandleMessage`, after the OBSERVE phase (around line 344), add checkpoint saving logic:

```go
		if ca.checkpointStore != nil && obsResult != nil {
			obsJSON, _ := json.Marshal(obsResult.Observations)
			planJSON, _ := json.Marshal(plan)
			cp := &TaskCheckpoint{
				ID:               fmt.Sprintf("cp-%s-%d", sess.ID(), attempt),
				SessionID:        sess.ID(),
				SubTaskIndex:     len(obsResult.Observations),
				ObservationsJSON: string(obsJSON),
				PlanJSON:         string(planJSON),
			}
			_ = ca.checkpointStore.Save(ctx, cp)
		}
```

Add `"encoding/json"` to imports if not already present.

- [ ] **Step 3: Add checkpoint cleanup on success**

At the `persist:` label (around line 413), before session persist, add:

```go
	if ca.checkpointStore != nil {
		_ = ca.checkpointStore.Delete(ctx, sess.ID())
	}
```

- [ ] **Step 4: Wire checkpoint store in gateway**

In `internal/gateway/gateway.go` (or wherever `CognitiveAgent` is constructed), after creating the cognitive agent, add:

```go
	checkpointStore := agent.NewSQLiteCheckpointStore(gw.db)
	cogAgent.SetCheckpointStore(checkpointStore)
```

- [ ] **Step 5: Build and verify**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: clean build

- [ ] **Step 6: Run full agent test suite**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/agent/ -v -count=1`
Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add internal/agent/cognitive.go internal/gateway/gateway.go
git commit -m "feat(agent): wire checkpoint save/clear into cognitive loop for task resume"
```

---

### Task 15: Dynamic Context Budget (C3)

**Files:**
- Create: `internal/agent/context_budget.go`
- Create: `internal/agent/context_budget_test.go`
- Modify: `internal/agent/perceive.go`

- [ ] **Step 1: Write the failing test**

Create `internal/agent/context_budget_test.go`:

```go
package agent

import (
	"testing"
)

func TestContextBudgetAllocator_Simple(t *testing.T) {
	alloc := NewContextBudgetAllocator()
	budget := alloc.Allocate(ComplexitySimple)

	if budget.MemoryLimit != 3 {
		t.Errorf("MemoryLimit = %d, want 3", budget.MemoryLimit)
	}
	if budget.KBLimit != 0 {
		t.Errorf("KBLimit = %d, want 0", budget.KBLimit)
	}
	if budget.IncludeProjectContext != true {
		t.Error("expected IncludeProjectContext = true for simple")
	}
	if budget.IncludeGraph {
		t.Error("expected IncludeGraph = false for simple")
	}
	if budget.IncludeGitState {
		t.Error("expected IncludeGitState = false for simple")
	}
}

func TestContextBudgetAllocator_Moderate(t *testing.T) {
	alloc := NewContextBudgetAllocator()
	budget := alloc.Allocate(ComplexityModerate)

	if budget.MemoryLimit != 5 {
		t.Errorf("MemoryLimit = %d, want 5", budget.MemoryLimit)
	}
	if budget.KBLimit != 3 {
		t.Errorf("KBLimit = %d, want 3", budget.KBLimit)
	}
	if !budget.IncludeProjectContext {
		t.Error("expected IncludeProjectContext = true")
	}
}

func TestContextBudgetAllocator_Complex(t *testing.T) {
	alloc := NewContextBudgetAllocator()
	budget := alloc.Allocate(ComplexityComplex)

	if budget.MemoryLimit != 10 {
		t.Errorf("MemoryLimit = %d, want 10", budget.MemoryLimit)
	}
	if budget.KBLimit != 5 {
		t.Errorf("KBLimit = %d, want 5", budget.KBLimit)
	}
	if !budget.IncludeGraph {
		t.Error("expected IncludeGraph = true for complex")
	}
	if !budget.IncludeGitState {
		t.Error("expected IncludeGitState = true for complex")
	}
}

func TestApplyBudget_TruncatesMemories(t *testing.T) {
	alloc := NewContextBudgetAllocator()
	state := &CognitiveState{
		Goal: Goal{Complexity: ComplexitySimple},
	}

	for i := 0; i < 10; i++ {
		state.RelevantMemories = append(state.RelevantMemories, memorySearchResult(float64(10-i)))
	}
	state.KnowledgeContext = []string{"k1", "k2", "k3", "k4", "k5"}
	state.GraphContext = []string{"g1", "g2"}

	alloc.Apply(state)

	if len(state.RelevantMemories) != 3 {
		t.Errorf("memories = %d, want 3 (simple budget)", len(state.RelevantMemories))
	}
	if len(state.KnowledgeContext) != 0 {
		t.Errorf("knowledge = %d, want 0 (simple budget)", len(state.KnowledgeContext))
	}
	if len(state.GraphContext) != 0 {
		t.Errorf("graph = %d, want 0 (simple budget)", len(state.GraphContext))
	}
}
```

We need a helper to create `memory.SearchResult` values for the test. Add this helper at the bottom of the test file:

```go
func memorySearchResult(score float64) memory.SearchResult {
	return memory.SearchResult{
		Score: score,
		Entry: memory.Entry{Content: "test memory"},
	}
}
```

Add `"github.com/Forest-Isle/IronClaw/internal/memory"` to the imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestContextBudgetAllocator ./internal/agent/ -v`
Expected: FAIL — `NewContextBudgetAllocator` undefined

- [ ] **Step 3: Implement the context budget allocator**

Create `internal/agent/context_budget.go`:

```go
package agent

// ContextBudget defines the limits for each context source based on complexity.
type ContextBudget struct {
	MemoryLimit           int
	KBLimit               int
	IncludeGraph          bool
	IncludeProjectContext bool
	IncludeGitState       bool
}

type ContextBudgetAllocator struct{}

func NewContextBudgetAllocator() *ContextBudgetAllocator {
	return &ContextBudgetAllocator{}
}

func (a *ContextBudgetAllocator) Allocate(complexity TaskComplexity) ContextBudget {
	switch complexity {
	case ComplexitySimple:
		return ContextBudget{
			MemoryLimit:           3,
			KBLimit:               0,
			IncludeGraph:          false,
			IncludeProjectContext: true,
			IncludeGitState:       false,
		}
	case ComplexityModerate:
		return ContextBudget{
			MemoryLimit:           5,
			KBLimit:               3,
			IncludeGraph:          false,
			IncludeProjectContext: true,
			IncludeGitState:       false,
		}
	case ComplexityComplex:
		return ContextBudget{
			MemoryLimit:           10,
			KBLimit:               5,
			IncludeGraph:          true,
			IncludeProjectContext: true,
			IncludeGitState:       true,
		}
	default:
		return ContextBudget{
			MemoryLimit:           5,
			KBLimit:               3,
			IncludeGraph:          false,
			IncludeProjectContext: true,
			IncludeGitState:       false,
		}
	}
}

// Apply trims CognitiveState context sources according to the budget.
func (a *ContextBudgetAllocator) Apply(state *CognitiveState) {
	budget := a.Allocate(state.Goal.Complexity)

	if len(state.RelevantMemories) > budget.MemoryLimit {
		state.RelevantMemories = state.RelevantMemories[:budget.MemoryLimit]
	}

	if len(state.KnowledgeContext) > budget.KBLimit {
		state.KnowledgeContext = state.KnowledgeContext[:budget.KBLimit]
	}

	if !budget.IncludeGraph {
		state.GraphContext = nil
	}

	if !budget.IncludeProjectContext {
		state.ProjectCtx = nil
	}

	if !budget.IncludeGitState {
		state.GitState = nil
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run "TestContextBudgetAllocator|TestApplyBudget" ./internal/agent/ -v`
Expected: all PASS

- [ ] **Step 5: Wire budget allocator into Perceiver**

In `internal/agent/perceive.go`, add a `budgetAlloc *ContextBudgetAllocator` field to `Perceiver` and a setter:

```go
func (p *Perceiver) SetBudgetAllocator(ba *ContextBudgetAllocator) {
	p.budgetAlloc = ba
}
```

At the end of `Perceiver.Run`, before returning `state`, add:

```go
	if p.budgetAlloc != nil {
		p.budgetAlloc.Apply(state)
	}
```

In `cognitive.go` `NewCognitiveAgent`, wire the allocator:

```go
	budgetAlloc := NewContextBudgetAllocator()
	ca.perceiver.SetBudgetAllocator(budgetAlloc)
```

- [ ] **Step 6: Build and verify**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: clean build

- [ ] **Step 7: Run full test suite**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/agent/ -v -count=1`
Expected: all PASS

- [ ] **Step 8: Commit**

```bash
git add internal/agent/context_budget.go internal/agent/context_budget_test.go internal/agent/perceive.go internal/agent/cognitive.go
git commit -m "feat(agent): dynamic context budget allocator based on task complexity"
```

---

## Self-Review

**Spec coverage:**

| Spec Item | Task |
|-----------|------|
| A1 — Task Checkpoints | Task 13 (store) + Task 14 (wiring) |
| A2 — Structured Verification | Task 1 (types) + Task 2 (logic) + Task 3 (observer integration) |
| A3 — Context-Aware Smart Retry | Task 5 (builder) + Task 6 (reflect wiring) |
| B1 — Structured Bash Output | Task 4 |
| B2 — Browser Search + Extract | Task 9 + Task 10 |
| B3 — Tool Result Cache | Task 11 |
| C1 — Project Context | Task 7 (scanner) + Task 8 (wiring) |
| C2 — Git State Awareness | Task 12 |
| C3 — Dynamic Context Budget | Task 15 |

All 9 spec items are covered.

**Type consistency check:**

- `AssertionResult` — defined in Task 1, used in Tasks 2/3/5/6 ✓
- `FailureContext` — defined in Task 1, used in Tasks 3/5/6 ✓
- `ProjectContext` — defined in Task 7, used in Tasks 8/15 ✓
- `GitState` — defined in Task 12, used in Tasks 12/15 ✓
- `TaskCheckpoint` — defined in Task 13, used in Task 14 ✓
- `ContextBudget` — defined in Task 15, used in Task 15 ✓
- `CheckpointStore` — interface defined in Task 13, implemented and wired in Tasks 13/14 ✓
- `generateAssertions()` — defined in Task 2, called in Task 3 ✓
- `enrichFailureContexts()` / `formatFailureContextForPrompt()` — defined in Task 5, called in Task 6 ✓
- `buildReflectUserMessage` — signature change in Task 6 adds `replanAttempt int` — caller updated in same task ✓
