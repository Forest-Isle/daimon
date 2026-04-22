package eval

import (
	"context"
	"os"
	"testing"
	"time"
)

type mockRunner struct {
	results map[string]*EvalResult
}

func (m *mockRunner) RunTask(_ context.Context, task TaskCase) (*EvalResult, error) {
	if r, ok := m.results[task.ID]; ok {
		return r, nil
	}
	return &EvalResult{
		TaskID:    task.ID,
		Goal:      task.Goal,
		Success:   true,
		Duration:  100 * time.Millisecond,
		Timestamp: time.Now(),
	}, nil
}

func TestRunSuite_Basic(t *testing.T) {
	runner := &mockRunner{
		results: map[string]*EvalResult{
			"t1": {TaskID: "t1", Success: true, Duration: 50 * time.Millisecond, Confidence: 0.9, AssertionPassRate: 1.0, Timestamp: time.Now()},
			"t2": {TaskID: "t2", Success: false, Duration: 200 * time.Millisecond, Confidence: 0.3, AssertionPassRate: 0.5, Timestamp: time.Now()},
		},
	}

	tasks := []TaskCase{
		{ID: "t1", Goal: "test 1"},
		{ID: "t2", Goal: "test 2"},
	}

	suite, err := RunSuite(context.Background(), "test-run", tasks, runner)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}

	if len(suite.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(suite.Results))
	}

	summary := suite.Summary()
	if summary.Passed != 1 || summary.Failed != 1 {
		t.Errorf("passed=%d failed=%d, want 1/1", summary.Passed, summary.Failed)
	}
	if summary.SuccessRate != 0.5 {
		t.Errorf("success_rate=%.2f, want 0.5", summary.SuccessRate)
	}
}

func TestRunSuite_Empty(t *testing.T) {
	runner := &mockRunner{}
	_, err := RunSuite(context.Background(), "empty", nil, runner)
	if err == nil {
		t.Error("expected error for empty tasks")
	}
}

func TestRunSuite_SuccessFunc(t *testing.T) {
	runner := &mockRunner{
		results: map[string]*EvalResult{
			"t1": {TaskID: "t1", Success: true, ToolsUsed: []string{"bash"}, Timestamp: time.Now()},
		},
	}

	tasks := []TaskCase{
		{
			ID:          "t1",
			Goal:        "test",
			ExpectTools: []string{"bash"},
			SuccessFunc: func(r *EvalResult) bool {
				return len(r.ToolsUsed) > 0 && r.ToolsUsed[0] == "bash"
			},
		},
	}

	suite, err := RunSuite(context.Background(), "func-check", tasks, runner)
	if err != nil {
		t.Fatal(err)
	}

	if !suite.Results[0].Success {
		t.Error("SuccessFunc should have set success to true")
	}
}

func TestCompare(t *testing.T) {
	before := &SuiteResult{
		RunID: "run-1",
		Results: []EvalResult{
			{TaskID: "t1", Success: true, AssertionPassRate: 0.8, Confidence: 0.7, ReplanCount: 2},
			{TaskID: "t2", Success: false, AssertionPassRate: 0.5, Confidence: 0.3, ReplanCount: 1},
		},
		Duration: 5 * time.Second,
	}
	after := &SuiteResult{
		RunID: "run-2",
		Results: []EvalResult{
			{TaskID: "t1", Success: true, AssertionPassRate: 1.0, Confidence: 0.9, ReplanCount: 0},
			{TaskID: "t2", Success: true, AssertionPassRate: 0.9, Confidence: 0.8, ReplanCount: 0},
		},
		Duration: 3 * time.Second,
	}

	report := Compare(before, after)

	if report.Deltas.SuccessRateDelta != 0.5 {
		t.Errorf("success_rate_delta=%.2f, want 0.5", report.Deltas.SuccessRateDelta)
	}
	if report.Deltas.AvgReplanCountDelta >= 0 {
		t.Error("expected replan count to decrease")
	}

	md := report.FormatMarkdown()
	if md == "" {
		t.Error("expected non-empty markdown")
	}
}

func TestSuiteSummary_ZeroResults(t *testing.T) {
	suite := &SuiteResult{RunID: "empty"}
	summary := suite.Summary()
	if summary.SuccessRate != 0 {
		t.Errorf("expected 0 success rate for empty suite, got %f", summary.SuccessRate)
	}
}

func TestTaskCase_NewFields_BackwardCompatible(t *testing.T) {
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

	exitCode := 0
	task := TaskCase{
		ID:           "new-style",
		Goal:         "test with reference",
		Complexity:   "moderate",
		Dimension:    DimPlanning,
		VerifyMethod: VerifyHybrid,
		Reference: &Reference{
			Answer:         "hello world",
			MustContain:    []string{"hello"},
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
			{TaskID: "t1", Success: true, FinalScore: 0.96, Dimension: DimTaskExecution},
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
	if len(report.Regressions) > 0 && report.Regressions[0].TaskID != "t2" {
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

func TestEvalResult_NewFields(t *testing.T) {
	result := EvalResult{
		TaskID:    "test",
		Success:   true,
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

// TestRunSuiteWithOptions_SuccessFuncBeforeCleanup verifies that SuccessFunc runs
// before CleanupFunc so file-based checks see agent-written files.
func TestRunSuiteWithOptions_SuccessFuncBeforeCleanup(t *testing.T) {
	tmpFile := t.TempDir() + "/marker.txt"

	runner := &mockRunner{
		results: map[string]*EvalResult{
			"t1": {TaskID: "t1", Success: true, Duration: 10 * time.Millisecond, Timestamp: time.Now()},
		},
	}

	tasks := []TaskCase{
		{
			ID:   "t1",
			Goal: "write file then verify before cleanup",
			SetupFunc: func() error {
				return os.WriteFile(tmpFile, []byte("present"), 0o644)
			},
			CleanupFunc: func() error {
				return os.Remove(tmpFile)
			},
			// SuccessFunc checks the file exists — should see it because cleanup hasn't run yet.
			SuccessFunc: func(r *EvalResult) bool {
				_, err := os.Stat(tmpFile)
				return err == nil
			},
		},
	}

	suite, err := RunSuiteWithOptions(context.Background(), "order-test", tasks, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !suite.Results[0].Success {
		t.Error("SuccessFunc should have seen the file (must run before CleanupFunc)")
	}
	// File must be gone after the suite (cleanup ran).
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error("CleanupFunc should have removed the file")
	}
}
