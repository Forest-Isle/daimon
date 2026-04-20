# Eval Phase 1: Framework Upgrade Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade the eval harness to support multi-dimension scoring, LLM-as-Judge, deterministic verification, and per-task regression tracking.

**Architecture:** Extend existing `TaskCase`/`EvalResult` with optional new fields (backward-compatible). Add two new verification modules (`verifier.go` for deterministic checks, `judge.go` for LLM evaluation). Enhance `Compare` for per-task regression. Wire everything through `RunSuite` with a new `RunOptions` parameter.

**Tech Stack:** Go 1.24, `github.com/Forest-Isle/IronClaw`, CGO_ENABLED=1, `-tags fts5`

**Design Spec:** `docs/feature/EVAL_COMPREHENSIVE_UPGRADE.md`

---

### Task 1: Dimension Type Definitions

**Files:**
- Create: `internal/eval/dimension.go`
- Test: `internal/eval/dimension_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/eval/dimension_test.go
package eval

import "testing"

func TestDimension_String(t *testing.T) {
	tests := []struct {
		dim  Dimension
		want string
	}{
		{DimTaskExecution, "task_execution"},
		{DimPlanning, "planning"},
		{DimErrorRecovery, "error_recovery"},
		{DimToolSelection, "tool_selection"},
		{DimConversation, "conversation"},
		{DimMemory, "memory"},
		{DimKnowledge, "knowledge"},
		{DimMultiAgent, "multi_agent"},
	}
	for _, tt := range tests {
		if string(tt.dim) != tt.want {
			t.Errorf("Dimension %q != %q", tt.dim, tt.want)
		}
	}
}

func TestVerifyMethod_Values(t *testing.T) {
	if VerifyDeterministic != "deterministic" {
		t.Error("unexpected VerifyDeterministic value")
	}
	if VerifyLLMJudge != "llm_judge" {
		t.Error("unexpected VerifyLLMJudge value")
	}
	if VerifyHybrid != "hybrid" {
		t.Error("unexpected VerifyHybrid value")
	}
}

func TestDefaultDimension(t *testing.T) {
	if DefaultDimension("") != DimTaskExecution {
		t.Error("empty dimension should default to task_execution")
	}
	if DefaultDimension(DimPlanning) != DimPlanning {
		t.Error("non-empty dimension should remain unchanged")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestDimension ./internal/eval/ -v`
Expected: FAIL — `Dimension` type not defined

- [ ] **Step 3: Write dimension.go**

```go
// internal/eval/dimension.go
package eval

// Dimension categorizes evaluation tasks into capability areas.
type Dimension string

const (
	DimTaskExecution Dimension = "task_execution"
	DimPlanning      Dimension = "planning"
	DimErrorRecovery Dimension = "error_recovery"
	DimToolSelection Dimension = "tool_selection"
	DimConversation  Dimension = "conversation"
	DimMemory        Dimension = "memory"
	DimKnowledge     Dimension = "knowledge"
	DimMultiAgent    Dimension = "multi_agent"
)

// AllDimensions returns the full list of recognized dimensions.
func AllDimensions() []Dimension {
	return []Dimension{
		DimTaskExecution, DimPlanning, DimErrorRecovery, DimToolSelection,
		DimConversation, DimMemory, DimKnowledge, DimMultiAgent,
	}
}

// DefaultDimension returns DimTaskExecution when dim is empty, otherwise dim.
func DefaultDimension(dim Dimension) Dimension {
	if dim == "" {
		return DimTaskExecution
	}
	return dim
}

// VerifyMethod determines how a task's output is verified.
type VerifyMethod string

const (
	VerifyDeterministic VerifyMethod = "deterministic"
	VerifyLLMJudge      VerifyMethod = "llm_judge"
	VerifyHybrid        VerifyMethod = "hybrid"
)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run "TestDimension|TestVerifyMethod|TestDefaultDimension" ./internal/eval/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/eval/dimension.go internal/eval/dimension_test.go
git commit -m "eval: add Dimension and VerifyMethod type definitions"
```

---

### Task 2: Extend TaskCase with Reference, Rubric, and Dimension Fields

**Files:**
- Modify: `internal/eval/harness.go` (lines 11-22 — `TaskCase` struct)
- Test: `internal/eval/harness_test.go` (add new test)

- [ ] **Step 1: Write the failing test**

Add to `internal/eval/harness_test.go`:

```go
func TestTaskCase_NewFields_BackwardCompatible(t *testing.T) {
	// Old-style task without new fields still works.
	old := TaskCase{
		ID:          "legacy",
		Goal:        "test",
		Complexity:  "simple",
		Tags:        []string{"bash"},
		ExpectTools: []string{"bash"},
	}
	if old.Dimension != "" {
		t.Error("empty Dimension expected for legacy tasks")
	}
	if old.Reference != nil {
		t.Error("nil Reference expected for legacy tasks")
	}
	if old.Rubric != nil {
		t.Error("nil Rubric expected for legacy tasks")
	}

	// New-style task with all fields.
	exitCode := 0
	task := TaskCase{
		ID:          "new-style",
		Goal:        "test with reference",
		Complexity:  "moderate",
		Dimension:   DimPlanning,
		VerifyMethod: VerifyHybrid,
		Reference: &Reference{
			Answer:      "hello world",
			MustContain: []string{"hello"},
			MustNotContain: []string{"error"},
			FileChecks: []FileCheck{
				{Path: "/tmp/test.txt", MustExist: true, Contains: "hello"},
			},
			ExitCode: &exitCode,
		},
		Rubric: &Rubric{
			Criteria: []JudgeCriterion{
				{Name: "accuracy", Description: "Is the answer correct?", Weight: 0.6},
				{Name: "clarity", Description: "Is it clear?", Weight: 0.4},
			},
		},
	}

	if task.Dimension != DimPlanning {
		t.Error("Dimension should be planning")
	}
	if len(task.Reference.MustContain) != 1 {
		t.Error("MustContain should have 1 entry")
	}
	if len(task.Rubric.Criteria) != 2 {
		t.Error("Rubric should have 2 criteria")
	}
	totalWeight := 0.0
	for _, c := range task.Rubric.Criteria {
		totalWeight += c.Weight
	}
	if totalWeight < 0.99 || totalWeight > 1.01 {
		t.Errorf("Rubric weights should sum to 1.0, got %f", totalWeight)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestTaskCase_NewFields ./internal/eval/ -v`
Expected: FAIL — `Reference`, `Rubric`, `JudgeCriterion`, `FileCheck` types not defined

- [ ] **Step 3: Extend TaskCase in harness.go**

In `internal/eval/harness.go`, replace the `TaskCase` struct (lines 11-22):

```go
// TaskCase defines a single evaluation task with success criteria.
type TaskCase struct {
	ID          string   `json:"id"`
	Goal        string   `json:"goal"`
	Complexity  string   `json:"complexity"`
	Tags        []string `json:"tags,omitempty"`
	ExpectTools []string `json:"expect_tools,omitempty"`

	// SuccessFunc is an optional programmatic check run after execution.
	// When nil the result relies on the agent's own reflection.
	SuccessFunc func(result *EvalResult) bool `json:"-"`

	// Dimension categorizes this task into a capability area for reporting.
	Dimension Dimension `json:"dimension,omitempty"`

	// VerifyMethod controls which verification layers run after execution.
	VerifyMethod VerifyMethod `json:"verify_method,omitempty"`

	// Reference provides ground truth for deterministic verification.
	Reference *Reference `json:"reference,omitempty"`

	// Rubric defines scoring criteria for LLM-as-Judge evaluation.
	Rubric *Rubric `json:"rubric,omitempty"`

	// SetupFunc runs before task execution to prepare the environment.
	SetupFunc func() error `json:"-"`

	// CleanupFunc runs after task execution to clean up.
	CleanupFunc func() error `json:"-"`
}

// Reference holds ground truth data for deterministic verification.
type Reference struct {
	Answer         string      `json:"answer,omitempty"`
	MustContain    []string    `json:"must_contain,omitempty"`
	MustNotContain []string    `json:"must_not_contain,omitempty"`
	FileChecks     []FileCheck `json:"file_checks,omitempty"`
	ExitCode       *int        `json:"exit_code,omitempty"`
}

// FileCheck verifies a file's existence and content.
type FileCheck struct {
	Path        string `json:"path"`
	MustExist   bool   `json:"must_exist"`
	Contains    string `json:"contains,omitempty"`
	NotContains string `json:"not_contains,omitempty"`
}

// Rubric defines the scoring criteria for LLM-as-Judge evaluation.
type Rubric struct {
	Criteria []JudgeCriterion `json:"criteria"`
}

// JudgeCriterion is a single scoring dimension within a Rubric.
type JudgeCriterion struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Weight      float64 `json:"weight"`
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestTaskCase_NewFields ./internal/eval/ -v`
Expected: PASS

Also run all existing tests to confirm backward compatibility:
Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/eval/ -v`
Expected: All existing tests still PASS

- [ ] **Step 5: Commit**

```bash
git add internal/eval/harness.go internal/eval/harness_test.go
git commit -m "eval: extend TaskCase with Dimension, Reference, Rubric fields"
```

---

### Task 3: Extend EvalResult with New Scoring Fields

**Files:**
- Modify: `internal/eval/harness.go` (lines 25-39 — `EvalResult` struct)
- Test: `internal/eval/harness_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/eval/harness_test.go`:

```go
func TestEvalResult_NewFields(t *testing.T) {
	result := EvalResult{
		TaskID:  "test",
		Success: true,
		Dimension: DimConversation,
		AgentOutput: "The answer is 42.",
		VerifyResult: &VerifyResult{
			Passed: true,
			Score:  1.0,
			Checks: []CheckResult{
				{Name: "must_contain:42", Passed: true, Detail: "found '42'"},
			},
		},
		JudgeResult: &JudgeResult{
			Scores:     map[string]float64{"accuracy": 0.9, "clarity": 0.8},
			Overall:    0.86,
			Reasoning:  "Good answer with clear explanation.",
			Weaknesses: []string{},
		},
		FinalScore:      0.93,
		FailureCategory: "",
	}

	if result.Dimension != DimConversation {
		t.Error("Dimension mismatch")
	}
	if result.VerifyResult.Score != 1.0 {
		t.Error("VerifyResult.Score mismatch")
	}
	if result.JudgeResult.Overall != 0.86 {
		t.Error("JudgeResult.Overall mismatch")
	}
	if result.FinalScore != 0.93 {
		t.Error("FinalScore mismatch")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestEvalResult_NewFields ./internal/eval/ -v`
Expected: FAIL — `VerifyResult`, `JudgeResult`, `CheckResult` types not defined

- [ ] **Step 3: Extend EvalResult in harness.go and add supporting types**

In `internal/eval/harness.go`, replace the `EvalResult` struct (lines 25-39):

```go
// EvalResult captures the outcome of running one TaskCase.
type EvalResult struct {
	TaskID            string        `json:"task_id"`
	Goal              string        `json:"goal"`
	Complexity        string        `json:"complexity"`
	Success           bool          `json:"success"`
	Duration          time.Duration `json:"duration_ms"`
	ToolsUsed         []string      `json:"tools_used"`
	ReplanCount       int           `json:"replan_count"`
	AssertionTotal    int           `json:"assertion_total"`
	AssertionPassed   int           `json:"assertion_passed"`
	AssertionPassRate float64       `json:"assertion_pass_rate"`
	Confidence        float64       `json:"confidence"`
	Error             string        `json:"error,omitempty"`
	Timestamp         time.Time     `json:"timestamp"`

	// New scoring fields — all optional for backward compatibility.
	Dimension       Dimension       `json:"dimension,omitempty"`
	AgentOutput     string          `json:"agent_output,omitempty"`
	VerifyResult    *VerifyResult   `json:"verify_result,omitempty"`
	JudgeResult     *JudgeResult    `json:"judge_result,omitempty"`
	FinalScore      float64         `json:"final_score,omitempty"`
	FailureCategory string          `json:"failure_category,omitempty"`
}

// VerifyResult holds the output of deterministic verification.
type VerifyResult struct {
	Passed bool          `json:"passed"`
	Checks []CheckResult `json:"checks"`
	Score  float64       `json:"score"`
}

// CheckResult is one check within a VerifyResult.
type CheckResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

// JudgeResult holds the output of LLM-as-Judge evaluation.
type JudgeResult struct {
	Scores     map[string]float64 `json:"scores"`
	Overall    float64            `json:"overall"`
	Reasoning  string             `json:"reasoning"`
	Weaknesses []string           `json:"weaknesses,omitempty"`
}
```

- [ ] **Step 4: Run all tests**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/eval/ -v`
Expected: All PASS (including existing tests)

- [ ] **Step 5: Commit**

```bash
git add internal/eval/harness.go internal/eval/harness_test.go
git commit -m "eval: extend EvalResult with VerifyResult, JudgeResult, FinalScore"
```

---

### Task 4: Deterministic Verifier Module

**Files:**
- Create: `internal/eval/verifier.go`
- Create: `internal/eval/verifier_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/eval/verifier_test.go
package eval

import "testing"

func TestVerifyReference_MustContain(t *testing.T) {
	task := TaskCase{
		Reference: &Reference{
			MustContain: []string{"hello", "world"},
		},
	}
	result := VerifyReference(task, "hello world foo")
	if !result.Passed {
		t.Error("should pass — output contains both words")
	}
	if result.Score != 1.0 {
		t.Errorf("score = %f, want 1.0", result.Score)
	}
	if len(result.Checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(result.Checks))
	}
}

func TestVerifyReference_MustContain_Partial(t *testing.T) {
	task := TaskCase{
		Reference: &Reference{
			MustContain: []string{"hello", "missing"},
		},
	}
	result := VerifyReference(task, "hello world")
	if result.Passed {
		t.Error("should fail — 'missing' not found")
	}
	if result.Score != 0.5 {
		t.Errorf("score = %f, want 0.5", result.Score)
	}
}

func TestVerifyReference_MustNotContain(t *testing.T) {
	task := TaskCase{
		Reference: &Reference{
			MustNotContain: []string{"error", "panic"},
		},
	}
	result := VerifyReference(task, "error occurred")
	if result.Passed {
		t.Error("should fail — output contains 'error'")
	}
}

func TestVerifyReference_FileChecks(t *testing.T) {
	task := TaskCase{
		Reference: &Reference{
			FileChecks: []FileCheck{
				{Path: "/tmp/ironclaw_verifier_test_nonexistent_12345.txt", MustExist: true},
			},
		},
	}
	result := VerifyReference(task, "")
	if result.Passed {
		t.Error("should fail — file does not exist")
	}
	if result.Checks[0].Passed {
		t.Error("file check should fail")
	}
}

func TestVerifyReference_ExitCode(t *testing.T) {
	exitCode := 0
	task := TaskCase{
		Reference: &Reference{
			ExitCode: &exitCode,
		},
	}
	// Agent output contains exit_code in structured bash output
	result := VerifyReference(task, `{"exit_code": 0, "stdout": "ok"}`)
	if !result.Passed {
		t.Error("should pass — exit_code matches")
	}
}

func TestVerifyReference_NilReference(t *testing.T) {
	task := TaskCase{}
	result := VerifyReference(task, "anything")
	if result == nil {
		t.Fatal("should return non-nil result even without reference")
	}
	if !result.Passed {
		t.Error("no reference means verification passes vacuously")
	}
	if result.Score != 1.0 {
		t.Errorf("no-reference score should be 1.0, got %f", result.Score)
	}
}

func TestVerifyReference_AnswerMatch(t *testing.T) {
	task := TaskCase{
		Reference: &Reference{
			Answer: "42",
		},
	}
	result := VerifyReference(task, "The answer is 42.")
	if !result.Passed {
		t.Error("should pass — output contains reference answer")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestVerifyReference ./internal/eval/ -v`
Expected: FAIL — `VerifyReference` function not defined

- [ ] **Step 3: Implement verifier.go**

```go
// internal/eval/verifier.go
package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// VerifyReference runs deterministic checks against agent output using the
// task's Reference. Returns a vacuous pass when Reference is nil.
func VerifyReference(task TaskCase, agentOutput string) *VerifyResult {
	if task.Reference == nil {
		return &VerifyResult{Passed: true, Score: 1.0}
	}

	ref := task.Reference
	var checks []CheckResult

	if ref.Answer != "" {
		passed := strings.Contains(agentOutput, ref.Answer)
		checks = append(checks, CheckResult{
			Name:   "answer_contains",
			Passed: passed,
			Detail: fmt.Sprintf("looking for %q in output", ref.Answer),
		})
	}

	for _, s := range ref.MustContain {
		passed := strings.Contains(agentOutput, s)
		detail := fmt.Sprintf("found %q", s)
		if !passed {
			detail = fmt.Sprintf("%q not found in output", s)
		}
		checks = append(checks, CheckResult{
			Name:   "must_contain:" + s,
			Passed: passed,
			Detail: detail,
		})
	}

	for _, s := range ref.MustNotContain {
		passed := !strings.Contains(agentOutput, s)
		detail := "absent as expected"
		if !passed {
			detail = fmt.Sprintf("unwanted %q found in output", s)
		}
		checks = append(checks, CheckResult{
			Name:   "must_not_contain:" + s,
			Passed: passed,
			Detail: detail,
		})
	}

	for _, fc := range ref.FileChecks {
		checks = append(checks, verifyFileCheck(fc)...)
	}

	if ref.ExitCode != nil {
		checks = append(checks, verifyExitCode(*ref.ExitCode, agentOutput))
	}

	if len(checks) == 0 {
		return &VerifyResult{Passed: true, Score: 1.0}
	}

	passedCount := 0
	for _, c := range checks {
		if c.Passed {
			passedCount++
		}
	}
	score := float64(passedCount) / float64(len(checks))
	allPassed := passedCount == len(checks)

	return &VerifyResult{
		Passed: allPassed,
		Checks: checks,
		Score:  score,
	}
}

func verifyFileCheck(fc FileCheck) []CheckResult {
	var checks []CheckResult

	info, err := os.Stat(fc.Path)
	exists := err == nil && !info.IsDir()

	if fc.MustExist {
		checks = append(checks, CheckResult{
			Name:   "file_exists:" + fc.Path,
			Passed: exists,
			Detail: fmt.Sprintf("exists=%v", exists),
		})
	}

	if exists && fc.Contains != "" {
		data, err := os.ReadFile(fc.Path)
		passed := err == nil && strings.Contains(string(data), fc.Contains)
		checks = append(checks, CheckResult{
			Name:   "file_contains:" + fc.Path,
			Passed: passed,
			Detail: fmt.Sprintf("looking for %q", fc.Contains),
		})
	}

	if exists && fc.NotContains != "" {
		data, err := os.ReadFile(fc.Path)
		passed := err == nil && !strings.Contains(string(data), fc.NotContains)
		checks = append(checks, CheckResult{
			Name:   "file_not_contains:" + fc.Path,
			Passed: passed,
			Detail: fmt.Sprintf("unwanted %q", fc.NotContains),
		})
	}

	return checks
}

func verifyExitCode(expected int, agentOutput string) CheckResult {
	var parsed struct {
		ExitCode *int `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(agentOutput), &parsed); err != nil || parsed.ExitCode == nil {
		return CheckResult{
			Name:   "exit_code",
			Passed: false,
			Detail: "could not parse exit_code from output",
		}
	}
	passed := *parsed.ExitCode == expected
	return CheckResult{
		Name:   "exit_code",
		Passed: passed,
		Detail: fmt.Sprintf("expected %d, got %d", expected, *parsed.ExitCode),
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestVerifyReference ./internal/eval/ -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/eval/verifier.go internal/eval/verifier_test.go
git commit -m "eval: add deterministic verifier module"
```

---

### Task 5: LLM Judge Module

**Files:**
- Create: `internal/eval/judge.go`
- Create: `internal/eval/judge_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/eval/judge_test.go
package eval

import (
	"context"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/agent"
)

type mockProvider struct {
	response string
}

func (m *mockProvider) Complete(_ context.Context, req agent.CompletionRequest) (*agent.CompletionResponse, error) {
	return &agent.CompletionResponse{
		Text:       m.response,
		StopReason: agent.StopEndTurn,
	}, nil
}

func (m *mockProvider) Stream(_ context.Context, req agent.CompletionRequest) (agent.StreamIterator, error) {
	return nil, nil
}

func TestLLMJudge_Judge_ValidResponse(t *testing.T) {
	provider := &mockProvider{
		response: `{"scores": {"accuracy": 0.9, "clarity": 0.8}, "overall": 0.86, "reasoning": "Good answer.", "weaknesses": ["could be more detailed"]}`,
	}
	judge := NewLLMJudge(provider)

	task := TaskCase{
		Goal: "Explain what Go interfaces are",
		Rubric: &Rubric{
			Criteria: []JudgeCriterion{
				{Name: "accuracy", Description: "Is it correct?", Weight: 0.6},
				{Name: "clarity", Description: "Is it clear?", Weight: 0.4},
			},
		},
	}

	result, err := judge.Judge(context.Background(), task, "Go interfaces define method sets...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Overall != 0.86 {
		t.Errorf("Overall = %f, want 0.86", result.Overall)
	}
	if result.Scores["accuracy"] != 0.9 {
		t.Errorf("accuracy = %f, want 0.9", result.Scores["accuracy"])
	}
	if len(result.Weaknesses) != 1 {
		t.Errorf("expected 1 weakness, got %d", len(result.Weaknesses))
	}
}

func TestLLMJudge_Judge_MalformedResponse(t *testing.T) {
	provider := &mockProvider{
		response: "This is not JSON at all",
	}
	judge := NewLLMJudge(provider)

	task := TaskCase{
		Goal: "test",
		Rubric: &Rubric{
			Criteria: []JudgeCriterion{
				{Name: "accuracy", Description: "correct?", Weight: 1.0},
			},
		},
	}

	result, err := judge.Judge(context.Background(), task, "some output")
	if err != nil {
		t.Fatalf("should not error on malformed response, got: %v", err)
	}
	if result.Overall != 0.5 {
		t.Errorf("malformed response should fallback to Overall=0.5, got %f", result.Overall)
	}
}

func TestLLMJudge_Judge_NilRubric(t *testing.T) {
	provider := &mockProvider{}
	judge := NewLLMJudge(provider)

	task := TaskCase{Goal: "test"}
	result, err := judge.Judge(context.Background(), task, "output")
	if err != nil {
		t.Fatal(err)
	}
	if result.Overall != 0.5 {
		t.Errorf("no rubric should return 0.5, got %f", result.Overall)
	}
}

func TestLLMJudge_BuildPrompt(t *testing.T) {
	judge := NewLLMJudge(nil)
	task := TaskCase{
		Goal: "Explain channels in Go",
		Reference: &Reference{Answer: "Channels are typed conduits"},
		Rubric: &Rubric{
			Criteria: []JudgeCriterion{
				{Name: "accuracy", Description: "correct?", Weight: 1.0},
			},
		},
	}
	prompt := judge.buildPrompt(task, "My answer about channels")
	if prompt == "" {
		t.Error("prompt should not be empty")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestLLMJudge ./internal/eval/ -v`
Expected: FAIL — `NewLLMJudge` not defined

- [ ] **Step 3: Implement judge.go**

```go
// internal/eval/judge.go
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/agent"
)

// LLMJudge evaluates agent output quality using an LLM provider.
type LLMJudge struct {
	provider agent.Provider
}

// NewLLMJudge creates a judge backed by the given LLM provider.
func NewLLMJudge(provider agent.Provider) *LLMJudge {
	return &LLMJudge{provider: provider}
}

// Judge evaluates agentOutput against the task's Rubric criteria.
// Returns a fallback result (Overall=0.5) when Rubric is nil or LLM response
// cannot be parsed.
func (j *LLMJudge) Judge(ctx context.Context, task TaskCase, agentOutput string) (*JudgeResult, error) {
	if task.Rubric == nil || len(task.Rubric.Criteria) == 0 {
		return &JudgeResult{
			Scores:  map[string]float64{},
			Overall: 0.5,
			Reasoning: "No rubric provided; default score assigned.",
		}, nil
	}

	if j.provider == nil {
		return &JudgeResult{
			Scores:  map[string]float64{},
			Overall: 0.5,
			Reasoning: "No LLM provider configured for judge.",
		}, nil
	}

	prompt := j.buildPrompt(task, agentOutput)

	resp, err := j.provider.Complete(ctx, agent.CompletionRequest{
		System:    "You are an evaluation judge. Score the agent output against the given criteria. Respond ONLY with a JSON object.",
		Messages:  []agent.CompletionMessage{{Role: "user", Content: prompt}},
		MaxTokens: 1024,
	})
	if err != nil {
		return nil, fmt.Errorf("judge LLM call: %w", err)
	}

	result := j.parseResponse(resp.Text, task.Rubric)
	return result, nil
}

func (j *LLMJudge) buildPrompt(task TaskCase, agentOutput string) string {
	var b strings.Builder

	b.WriteString("## Task\n")
	b.WriteString(task.Goal)
	b.WriteString("\n\n")

	if task.Reference != nil && task.Reference.Answer != "" {
		b.WriteString("## Reference Answer\n")
		b.WriteString(task.Reference.Answer)
		b.WriteString("\n\n")
	}

	b.WriteString("## Agent Output\n")
	b.WriteString(agentOutput)
	b.WriteString("\n\n")

	b.WriteString("## Scoring Criteria\n")
	for _, c := range task.Rubric.Criteria {
		fmt.Fprintf(&b, "- **%s** (weight %.1f): %s\n", c.Name, c.Weight, c.Description)
	}

	b.WriteString("\n## Instructions\n")
	b.WriteString("Score each criterion from 0.0 to 1.0. Respond with a JSON object:\n")
	b.WriteString("```json\n")
	b.WriteString(`{"scores": {"criterion_name": 0.0-1.0, ...}, "overall": 0.0-1.0, "reasoning": "...", "weaknesses": ["..."]}`)
	b.WriteString("\n```\n")
	b.WriteString("The 'overall' should be the weighted average of all criterion scores.")

	return b.String()
}

func (j *LLMJudge) parseResponse(text string, rubric *Rubric) *JudgeResult {
	text = extractJSON(text)

	var result JudgeResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		slog.Warn("judge: failed to parse LLM response, using fallback", "err", err)
		return &JudgeResult{
			Scores:    map[string]float64{},
			Overall:   0.5,
			Reasoning: "Failed to parse judge response; fallback score assigned.",
		}
	}

	if result.Scores == nil {
		result.Scores = map[string]float64{}
	}

	// Recompute Overall from weights if scores are present.
	if len(result.Scores) > 0 && rubric != nil {
		weighted := 0.0
		totalWeight := 0.0
		for _, c := range rubric.Criteria {
			if s, ok := result.Scores[c.Name]; ok {
				weighted += s * c.Weight
				totalWeight += c.Weight
			}
		}
		if totalWeight > 0 {
			result.Overall = weighted / totalWeight
		}
	}

	return &result
}

// extractJSON attempts to extract a JSON object from text that may contain
// markdown code fences or other surrounding text.
func extractJSON(text string) string {
	text = strings.TrimSpace(text)

	if idx := strings.Index(text, "```json"); idx >= 0 {
		text = text[idx+7:]
		if end := strings.Index(text, "```"); end >= 0 {
			text = text[:end]
		}
	} else if idx := strings.Index(text, "```"); idx >= 0 {
		text = text[idx+3:]
		if end := strings.Index(text, "```"); end >= 0 {
			text = text[:end]
		}
	}

	text = strings.TrimSpace(text)

	if start := strings.Index(text, "{"); start >= 0 {
		depth := 0
		for i := start; i < len(text); i++ {
			if text[i] == '{' {
				depth++
			} else if text[i] == '}' {
				depth--
				if depth == 0 {
					return text[start : i+1]
				}
			}
		}
	}

	return text
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestLLMJudge ./internal/eval/ -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/eval/judge.go internal/eval/judge_test.go
git commit -m "eval: add LLM-as-Judge module"
```

---

### Task 6: FinalScore Computation + RunSuite Integration

**Files:**
- Modify: `internal/eval/harness.go` (RunSuite function + new helpers)
- Modify: `internal/eval/harness_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/eval/harness_test.go`:

```go
func TestComputeFinalScore_Deterministic(t *testing.T) {
	vr := &VerifyResult{Score: 0.8}
	score := ComputeFinalScore(VerifyDeterministic, vr, nil, 0.0)
	if score != 0.8 {
		t.Errorf("score = %f, want 0.8", score)
	}
}

func TestComputeFinalScore_LLMJudge(t *testing.T) {
	jr := &JudgeResult{Overall: 0.9}
	score := ComputeFinalScore(VerifyLLMJudge, nil, jr, 0.0)
	if score != 0.9 {
		t.Errorf("score = %f, want 0.9", score)
	}
}

func TestComputeFinalScore_Hybrid(t *testing.T) {
	vr := &VerifyResult{Score: 0.8}
	jr := &JudgeResult{Overall: 0.6}
	score := ComputeFinalScore(VerifyHybrid, vr, jr, 0.0)
	want := 0.5*0.8 + 0.5*0.6
	if diff := score - want; diff > 0.001 || diff < -0.001 {
		t.Errorf("score = %f, want %f", score, want)
	}
}

func TestComputeFinalScore_Legacy(t *testing.T) {
	score := ComputeFinalScore("", nil, nil, 0.75)
	if score != 0.75 {
		t.Errorf("legacy score = %f, want 0.75", score)
	}
}

func TestRunSuiteWithOptions_SetupCleanup(t *testing.T) {
	setupCalled := false
	cleanupCalled := false

	runner := &mockRunner{
		results: map[string]*EvalResult{
			"t1": {TaskID: "t1", Success: true, Duration: 50 * time.Millisecond, Timestamp: time.Now()},
		},
	}

	tasks := []TaskCase{
		{
			ID:   "t1",
			Goal: "test with setup",
			SetupFunc: func() error {
				setupCalled = true
				return nil
			},
			CleanupFunc: func() error {
				cleanupCalled = true
				return nil
			},
		},
	}

	_, err := RunSuiteWithOptions(context.Background(), "test", tasks, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !setupCalled {
		t.Error("SetupFunc was not called")
	}
	if !cleanupCalled {
		t.Error("CleanupFunc was not called")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run "TestComputeFinalScore|TestRunSuiteWithOptions" ./internal/eval/ -v`
Expected: FAIL — functions not defined

- [ ] **Step 3: Add ComputeFinalScore and RunSuiteWithOptions to harness.go**

Add after the existing `RunSuite` function in `internal/eval/harness.go`:

```go
// RunOptions configures optional behavior for RunSuiteWithOptions.
type RunOptions struct {
	Judge *LLMJudge
}

// RunSuiteWithOptions extends RunSuite with verification, judging, and setup/cleanup.
// Passing nil options is equivalent to calling RunSuite.
func RunSuiteWithOptions(ctx context.Context, runID string, tasks []TaskCase, runner AgentRunner, opts *RunOptions) (*SuiteResult, error) {
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no tasks to evaluate")
	}

	suite := &SuiteResult{
		RunID:     runID,
		Results:   make([]EvalResult, 0, len(tasks)),
		StartedAt: time.Now(),
	}

	if sc, ok := runner.(SnapshotCaptor); ok {
		suite.EvoBefore = sc.CaptureSnapshot()
	}

	for i, task := range tasks {
		select {
		case <-ctx.Done():
			return suite, ctx.Err()
		default:
		}

		if task.SetupFunc != nil {
			if err := task.SetupFunc(); err != nil {
				suite.Results = append(suite.Results, EvalResult{
					TaskID:    task.ID,
					Goal:      task.Goal,
					Error:     fmt.Sprintf("setup failed: %v", err),
					Dimension: DefaultDimension(task.Dimension),
					Timestamp: time.Now(),
				})
				continue
			}
		}

		result, err := runner.RunTask(ctx, task)

		if task.CleanupFunc != nil {
			_ = task.CleanupFunc()
		}

		if err != nil {
			suite.Results = append(suite.Results, EvalResult{
				TaskID:    task.ID,
				Goal:      task.Goal,
				Error:     err.Error(),
				Dimension: DefaultDimension(task.Dimension),
				Timestamp: time.Now(),
			})
			continue
		}

		result.Dimension = DefaultDimension(task.Dimension)

		if task.SuccessFunc != nil {
			result.Success = task.SuccessFunc(result)
		}

		agentOutput := result.AgentOutput

		var vr *VerifyResult
		if task.Reference != nil {
			vr = VerifyReference(task, agentOutput)
			result.VerifyResult = vr
		}

		var jr *JudgeResult
		if opts != nil && opts.Judge != nil && task.Rubric != nil &&
			(task.VerifyMethod == VerifyLLMJudge || task.VerifyMethod == VerifyHybrid) {
			var judgeErr error
			jr, judgeErr = opts.Judge.Judge(ctx, task, agentOutput)
			if judgeErr != nil {
				slog.Warn("judge failed for task", "task", task.ID, "err", judgeErr)
			} else {
				result.JudgeResult = jr
			}
		}

		result.FinalScore = ComputeFinalScore(task.VerifyMethod, vr, jr, result.AssertionPassRate)

		suite.Results = append(suite.Results, *result)

		fmt.Printf("  [%d/%d] %s — %s (%.1fs, score=%.2f)\n",
			i+1, len(tasks), task.ID, statusLabel(result.Success),
			result.Duration.Seconds(), result.FinalScore)
	}

	if sc, ok := runner.(SnapshotCaptor); ok {
		suite.EvoAfter = sc.CaptureSnapshot()
	}

	suite.Duration = time.Since(suite.StartedAt)
	return suite, nil
}

// ComputeFinalScore synthesizes a single score from verification and judge results.
func ComputeFinalScore(method VerifyMethod, vr *VerifyResult, jr *JudgeResult, assertionPassRate float64) float64 {
	switch method {
	case VerifyDeterministic:
		if vr != nil {
			return vr.Score
		}
		return assertionPassRate
	case VerifyLLMJudge:
		if jr != nil {
			return jr.Overall
		}
		return 0.5
	case VerifyHybrid:
		vs := assertionPassRate
		if vr != nil {
			vs = vr.Score
		}
		js := 0.5
		if jr != nil {
			js = jr.Overall
		}
		return 0.5*vs + 0.5*js
	default:
		return assertionPassRate
	}
}
```

Add `"log/slog"` to imports in harness.go if not already present.

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run "TestComputeFinalScore|TestRunSuiteWithOptions" ./internal/eval/ -v`
Expected: All PASS

Also run all tests:
Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/eval/ -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/eval/harness.go internal/eval/harness_test.go
git commit -m "eval: add ComputeFinalScore and RunSuiteWithOptions with setup/cleanup"
```

---

### Task 7: Per-Task Regression in Compare

**Files:**
- Modify: `internal/eval/compare.go`
- Modify: `internal/eval/harness_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/eval/harness_test.go`:

```go
func TestCompare_TaskRegressions(t *testing.T) {
	before := &SuiteResult{
		RunID: "run-1",
		Results: []EvalResult{
			{TaskID: "t1", Success: true, FinalScore: 0.9, Dimension: DimTaskExecution},
			{TaskID: "t2", Success: true, FinalScore: 0.8, Dimension: DimPlanning},
			{TaskID: "t3", Success: false, FinalScore: 0.3, Dimension: DimPlanning},
		},
		Duration: 5 * time.Second,
	}
	after := &SuiteResult{
		RunID: "run-2",
		Results: []EvalResult{
			{TaskID: "t1", Success: true, FinalScore: 0.95, Dimension: DimTaskExecution},
			{TaskID: "t2", Success: false, FinalScore: 0.4, Dimension: DimPlanning},
			{TaskID: "t3", Success: true, FinalScore: 0.7, Dimension: DimPlanning},
		},
		Duration: 3 * time.Second,
	}

	report := Compare(before, after)

	if len(report.TaskRegressions) != 3 {
		t.Fatalf("expected 3 task regressions, got %d", len(report.TaskRegressions))
	}

	if len(report.Regressions) != 1 {
		t.Errorf("expected 1 regression, got %d", len(report.Regressions))
	}
	if report.Regressions[0].TaskID != "t2" {
		t.Errorf("expected t2 to regress, got %s", report.Regressions[0].TaskID)
	}

	if len(report.Improvements) != 2 {
		t.Errorf("expected 2 improvements, got %d", len(report.Improvements))
	}
}

func TestCompare_DimensionDeltas(t *testing.T) {
	before := &SuiteResult{
		RunID: "run-1",
		Results: []EvalResult{
			{TaskID: "t1", FinalScore: 0.8, Dimension: DimTaskExecution},
			{TaskID: "t2", FinalScore: 0.4, Dimension: DimPlanning},
		},
	}
	after := &SuiteResult{
		RunID: "run-2",
		Results: []EvalResult{
			{TaskID: "t1", FinalScore: 0.9, Dimension: DimTaskExecution},
			{TaskID: "t2", FinalScore: 0.6, Dimension: DimPlanning},
		},
	}

	report := Compare(before, after)

	if len(report.DimensionDeltas) == 0 {
		t.Fatal("expected dimension deltas")
	}
	if delta, ok := report.DimensionDeltas[DimPlanning]; !ok || delta < 0.19 {
		t.Errorf("planning delta = %f, want ~0.2", delta)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run "TestCompare_Task|TestCompare_Dimension" ./internal/eval/ -v`
Expected: FAIL — `TaskRegressions`, `Regressions`, `Improvements`, `DimensionDeltas` fields not on `ComparisonReport`

- [ ] **Step 3: Enhance compare.go**

Replace the `ComparisonReport` struct and `Compare` function in `internal/eval/compare.go`:

```go
// TaskRegression tracks how a specific task's score changed between runs.
type TaskRegression struct {
	TaskID      string    `json:"task_id"`
	Dimension   Dimension `json:"dimension,omitempty"`
	BeforeScore float64   `json:"before_score"`
	AfterScore  float64   `json:"after_score"`
	Delta       float64   `json:"delta"`
	Status      string    `json:"status"` // "improved", "regressed", "stable"
}

// ComparisonReport compares two evaluation runs side by side.
type ComparisonReport struct {
	BeforeRunID     string                   `json:"before_run_id"`
	AfterRunID      string                   `json:"after_run_id"`
	Before          SuiteSummary             `json:"before"`
	After           SuiteSummary             `json:"after"`
	Deltas          ComparisonDelta          `json:"deltas"`
	TaskRegressions []TaskRegression         `json:"task_regressions,omitempty"`
	Regressions     []TaskRegression         `json:"regressions,omitempty"`
	Improvements    []TaskRegression         `json:"improvements,omitempty"`
	DimensionDeltas map[Dimension]float64    `json:"dimension_deltas,omitempty"`
	GeneratedAt     time.Time                `json:"generated_at"`
}

// Compare produces a side-by-side comparison of two suite results including
// per-task regression tracking and dimension-level deltas.
func Compare(before, after *SuiteResult) *ComparisonReport {
	bs := before.Summary()
	as := after.Summary()

	report := &ComparisonReport{
		BeforeRunID: before.RunID,
		AfterRunID:  after.RunID,
		Before:      bs,
		After:       as,
		Deltas: ComparisonDelta{
			SuccessRateDelta:       as.SuccessRate - bs.SuccessRate,
			AvgAssertPassRateDelta: as.AvgAssertionPassRate - bs.AvgAssertionPassRate,
			AvgConfidenceDelta:     as.AvgConfidence - bs.AvgConfidence,
			AvgReplanCountDelta:    as.AvgReplanCount - bs.AvgReplanCount,
			DurationDelta:          as.Duration - bs.Duration,
		},
		DimensionDeltas: make(map[Dimension]float64),
		GeneratedAt:     time.Now(),
	}

	beforeMap := make(map[string]EvalResult)
	for _, r := range before.Results {
		beforeMap[r.TaskID] = r
	}

	for _, ar := range after.Results {
		if br, ok := beforeMap[ar.TaskID]; ok {
			delta := ar.FinalScore - br.FinalScore
			status := "stable"
			if delta > 0.05 {
				status = "improved"
			} else if delta < -0.05 {
				status = "regressed"
			}

			tr := TaskRegression{
				TaskID:      ar.TaskID,
				Dimension:   ar.Dimension,
				BeforeScore: br.FinalScore,
				AfterScore:  ar.FinalScore,
				Delta:       delta,
				Status:      status,
			}
			report.TaskRegressions = append(report.TaskRegressions, tr)

			if status == "regressed" {
				report.Regressions = append(report.Regressions, tr)
			} else if status == "improved" {
				report.Improvements = append(report.Improvements, tr)
			}
		}
	}

	beforeDims := aggregateDimScores(before.Results)
	afterDims := aggregateDimScores(after.Results)
	for dim, afterScore := range afterDims {
		if beforeScore, ok := beforeDims[dim]; ok {
			report.DimensionDeltas[dim] = afterScore - beforeScore
		}
	}

	return report
}

func aggregateDimScores(results []EvalResult) map[Dimension]float64 {
	sums := make(map[Dimension]float64)
	counts := make(map[Dimension]int)
	for _, r := range results {
		dim := DefaultDimension(r.Dimension)
		sums[dim] += r.FinalScore
		counts[dim]++
	}
	avgs := make(map[Dimension]float64)
	for dim, sum := range sums {
		if counts[dim] > 0 {
			avgs[dim] = sum / float64(counts[dim])
		}
	}
	return avgs
}
```

Update `FormatMarkdown` to include task-level info — append after the existing table:

```go
// Add inside FormatMarkdown, after the "Overall" line:

	if len(r.Regressions) > 0 {
		b.WriteString("\n### Regressions\n\n")
		b.WriteString("| Task | Dimension | Before | After | Delta |\n")
		b.WriteString("|------|-----------|--------|-------|-------|\n")
		for _, tr := range r.Regressions {
			fmt.Fprintf(&b, "| %s | %s | %.2f | %.2f | %.2f |\n",
				tr.TaskID, tr.Dimension, tr.BeforeScore, tr.AfterScore, tr.Delta)
		}
	}

	if len(r.Improvements) > 0 {
		b.WriteString("\n### Improvements\n\n")
		b.WriteString("| Task | Dimension | Before | After | Delta |\n")
		b.WriteString("|------|-----------|--------|-------|-------|\n")
		for _, tr := range r.Improvements {
			fmt.Fprintf(&b, "| %s | %s | %.2f | %.2f | +%.2f |\n",
				tr.TaskID, tr.Dimension, tr.BeforeScore, tr.AfterScore, tr.Delta)
		}
	}

	if len(r.DimensionDeltas) > 0 {
		b.WriteString("\n### Dimension Changes\n\n")
		b.WriteString("| Dimension | Delta |\n")
		b.WriteString("|-----------|-------|\n")
		for dim, delta := range r.DimensionDeltas {
			fmt.Fprintf(&b, "| %s | %s |\n", dim, fmtDelta(delta, "", true))
		}
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/eval/ -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/eval/compare.go internal/eval/harness_test.go
git commit -m "eval: add per-task regression tracking and dimension deltas to Compare"
```

---

### Task 8: CLI Updates — `--judge` Flag and `eval diagnose` Skeleton

**Files:**
- Modify: `cmd/ironclaw/eval.go`

- [ ] **Step 1: Add `--judge` flag to `eval run`**

In `newEvalRunCmd`, add a `judge` bool flag and wire `RunSuiteWithOptions`:

```go
// Add to the var block at the top of newEvalRunCmd:
judge bool

// Add flag registration:
cmd.Flags().BoolVar(&judge, "judge", false, "enable LLM-as-Judge for tasks with Rubric")
```

In the RunE function, after creating the runner, build RunOptions:

```go
var runOpts *eval.RunOptions
if judge && live {
	// Reuse the gateway's LLM provider for judging.
	judgeProvider := gw.LLMProvider()
	if judgeProvider != nil {
		runOpts = &eval.RunOptions{
			Judge: eval.NewLLMJudge(judgeProvider),
		}
		fmt.Println("LLM Judge: enabled")
	}
}

// Replace RunSuite call with:
result, err := eval.RunSuiteWithOptions(ctx, runID, tasks, runner, runOpts)
```

- [ ] **Step 2: Add `eval diagnose` skeleton command**

Add to `newEvalCmd`:

```go
cmd.AddCommand(newEvalRunCmd(), newEvalCompareCmd(), newEvalListCmd(),
	newEvalLongitudinalCmd(), newEvalVisualizeCmd(), newEvalDiagnoseCmd())
```

Add the skeleton command:

```go
func newEvalDiagnoseCmd() *cobra.Command {
	var (
		suite      string
		outputDir  string
		live       bool
		judge      bool
		configPath string
	)

	cmd := &cobra.Command{
		Use:   "diagnose",
		Short: "Run evaluation and generate weakness diagnosis report",
		Long: `Runs the evaluation suite, classifies failures, aggregates dimension scores,
and generates a weakness report with optimization recommendations.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("eval diagnose: coming in Phase 3")
			return nil
		},
	}

	cmd.Flags().StringVar(&suite, "suite", "full", "suite name or JSON file path")
	cmd.Flags().StringVarP(&outputDir, "output", "o", "", "output directory for reports")
	cmd.Flags().BoolVar(&live, "live", false, "run against a live cognitive agent")
	cmd.Flags().BoolVar(&judge, "judge", true, "enable LLM-as-Judge")
	cmd.Flags().StringVarP(&configPath, "config", "c", "configs/ironclaw.yaml", "config file path")
	return cmd
}
```

- [ ] **Step 3: Verify build**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: Build succeeds

- [ ] **Step 4: Verify CLI help**

Run: `./ironclaw eval --help`
Expected: Shows `run`, `compare`, `list`, `longitudinal`, `visualize`, `diagnose` subcommands

Run: `./ironclaw eval run --help`
Expected: Shows `--judge` flag

- [ ] **Step 5: Commit**

```bash
git add cmd/ironclaw/eval.go
git commit -m "eval: add --judge flag to eval run and diagnose command skeleton"
```

---

### Task 9: Integration Test — Full Pipeline

**Files:**
- Create: `internal/eval/integration_test.go`

- [ ] **Step 1: Write integration test**

```go
// internal/eval/integration_test.go
package eval

import (
	"context"
	"testing"
	"time"
)

func TestFullPipeline_DryRun_WithVerification(t *testing.T) {
	exitCode := 0
	tasks := []TaskCase{
		{
			ID:          "verify-pass",
			Goal:        "echo hello world",
			Complexity:  "simple",
			Dimension:   DimTaskExecution,
			VerifyMethod: VerifyDeterministic,
			Reference: &Reference{
				MustContain: []string{"hello"},
			},
		},
		{
			ID:          "verify-fail",
			Goal:        "echo error",
			Complexity:  "simple",
			Dimension:   DimErrorRecovery,
			VerifyMethod: VerifyDeterministic,
			Reference: &Reference{
				MustNotContain: []string{"error"},
			},
		},
		{
			ID:          "judge-task",
			Goal:        "explain Go interfaces",
			Complexity:  "moderate",
			Dimension:   DimConversation,
			VerifyMethod: VerifyLLMJudge,
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "accuracy", Description: "correct?", Weight: 1.0},
				},
			},
		},
		{
			ID:          "hybrid-task",
			Goal:        "write hello to file",
			Complexity:  "moderate",
			Dimension:   DimPlanning,
			VerifyMethod: VerifyHybrid,
			Reference: &Reference{
				ExitCode: &exitCode,
			},
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "quality", Description: "good?", Weight: 1.0},
				},
			},
		},
		{
			ID:         "legacy-task",
			Goal:       "old style task",
			Complexity: "simple",
		},
	}

	runner := &mockRunnerWithOutput{
		outputs: map[string]string{
			"verify-pass": "hello world",
			"verify-fail": "error occurred",
			"judge-task":  "Go interfaces define method sets...",
			"hybrid-task": `{"exit_code": 0, "stdout": "ok"}`,
			"legacy-task": "done",
		},
	}

	mockJudge := NewLLMJudge(&mockProvider{
		response: `{"scores": {"accuracy": 0.8}, "overall": 0.8, "reasoning": "ok", "weaknesses": []}`,
	})

	opts := &RunOptions{Judge: mockJudge}
	suite, err := RunSuiteWithOptions(context.Background(), "integration-test", tasks, runner, opts)
	if err != nil {
		t.Fatalf("RunSuiteWithOptions: %v", err)
	}

	if len(suite.Results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(suite.Results))
	}

	// verify-pass: deterministic, should pass
	r0 := suite.Results[0]
	if r0.VerifyResult == nil || !r0.VerifyResult.Passed {
		t.Error("verify-pass should have passing VerifyResult")
	}
	if r0.FinalScore != 1.0 {
		t.Errorf("verify-pass FinalScore = %f, want 1.0", r0.FinalScore)
	}
	if r0.Dimension != DimTaskExecution {
		t.Errorf("verify-pass Dimension = %s, want task_execution", r0.Dimension)
	}

	// verify-fail: deterministic, should fail MustNotContain
	r1 := suite.Results[1]
	if r1.VerifyResult == nil || r1.VerifyResult.Passed {
		t.Error("verify-fail should have failing VerifyResult")
	}
	if r1.FinalScore != 0.0 {
		t.Errorf("verify-fail FinalScore = %f, want 0.0", r1.FinalScore)
	}

	// judge-task: LLM judge only
	r2 := suite.Results[2]
	if r2.JudgeResult == nil {
		t.Error("judge-task should have JudgeResult")
	}
	if r2.FinalScore != 0.8 {
		t.Errorf("judge-task FinalScore = %f, want 0.8", r2.FinalScore)
	}

	// legacy-task: no new fields, FinalScore falls back to AssertionPassRate
	r4 := suite.Results[4]
	if r4.Dimension != DimTaskExecution {
		t.Errorf("legacy-task should default to task_execution, got %s", r4.Dimension)
	}
}

type mockRunnerWithOutput struct {
	outputs map[string]string
}

func (m *mockRunnerWithOutput) RunTask(_ context.Context, task TaskCase) (*EvalResult, error) {
	output := m.outputs[task.ID]
	return &EvalResult{
		TaskID:            task.ID,
		Goal:              task.Goal,
		Complexity:        task.Complexity,
		Success:           true,
		Duration:          50 * time.Millisecond,
		ToolsUsed:         task.ExpectTools,
		AssertionPassRate: 1.0,
		Confidence:        0.9,
		AgentOutput:       output,
		Timestamp:         time.Now(),
	}, nil
}
```

- [ ] **Step 2: Run test**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestFullPipeline ./internal/eval/ -v`
Expected: PASS

- [ ] **Step 3: Run ALL tests to confirm nothing is broken**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/eval/ -v`
Expected: All PASS

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: Build succeeds

- [ ] **Step 4: Commit**

```bash
git add internal/eval/integration_test.go
git commit -m "eval: add integration test for full verification pipeline"
```

---

### Task 10: Final Verification

- [ ] **Step 1: Run full test suite**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/eval/ -v -count=1`
Expected: All tests PASS

- [ ] **Step 2: Build binary**

Run: `CGO_ENABLED=1 go build -tags "fts5" -o ironclaw ./cmd/ironclaw/`
Expected: Build succeeds

- [ ] **Step 3: Smoke test CLI**

Run: `./ironclaw eval list --suite all`
Expected: Lists all suites with tasks

Run: `./ironclaw eval run --suite builtin`
Expected: Dry run completes, shows results

Run: `./ironclaw eval diagnose --help`
Expected: Shows diagnose command help

- [ ] **Step 4: Commit and tag**

```bash
git add -A
git commit -m "eval: Phase 1 complete — multi-dimension scoring, LLM Judge, per-task regression"
```
