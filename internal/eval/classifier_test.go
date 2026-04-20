package eval

import (
	"context"
	"testing"
	"time"
)

func TestClassifyByRules_Timeout(t *testing.T) {
	c := NewFailureClassifier(nil, 1*time.Minute)
	task := TaskCase{ID: "t1"}
	result := &EvalResult{Duration: 2 * time.Minute, FinalScore: 0.3}
	cat := c.Classify(context.Background(), task, result)
	if cat != FailTimeout {
		t.Errorf("expected timeout, got %s", cat)
	}
}

func TestClassifyByRules_LoopRetry(t *testing.T) {
	c := NewFailureClassifier(nil, 0)
	task := TaskCase{ID: "t1"}
	result := &EvalResult{ReplanCount: 5, FinalScore: 0.3}
	cat := c.Classify(context.Background(), task, result)
	if cat != FailErrorLoopRetry {
		t.Errorf("expected error_loop_retry, got %s", cat)
	}
}

func TestClassifyByRules_Hallucination(t *testing.T) {
	c := NewFailureClassifier(nil, 0)
	task := TaskCase{ID: "t1"}
	result := &EvalResult{
		FinalScore: 0.3,
		VerifyResult: &VerifyResult{
			Passed: false,
			Checks: []CheckResult{
				{Name: "must_not_contain_world", Passed: false},
			},
		},
	}
	cat := c.Classify(context.Background(), task, result)
	if cat != FailHallucination {
		t.Errorf("expected hallucination, got %s", cat)
	}
}

func TestClassifyByRules_ToolMisuse(t *testing.T) {
	c := NewFailureClassifier(nil, 0)
	task := TaskCase{ID: "t1", ExpectTools: []string{"file_read", "bash"}}
	result := &EvalResult{
		FinalScore: 0.3,
		ToolsUsed:  []string{"http"},
	}
	cat := c.Classify(context.Background(), task, result)
	if cat != FailToolMisuse {
		t.Errorf("expected tool_misuse, got %s", cat)
	}
}

func TestClassifyByRules_WrongAnswer(t *testing.T) {
	c := NewFailureClassifier(nil, 0)
	task := TaskCase{ID: "t1"}
	result := &EvalResult{
		FinalScore: 0.3,
		VerifyResult: &VerifyResult{
			Passed: false,
			Score:  0.2,
			Checks: []CheckResult{{Name: "must_contain_x", Passed: false}},
		},
	}
	cat := c.Classify(context.Background(), task, result)
	if cat != FailWrongAnswer {
		t.Errorf("expected wrong_answer, got %s", cat)
	}
}

func TestClassifyByRules_ErrorNoRecovery(t *testing.T) {
	c := NewFailureClassifier(nil, 0)
	task := TaskCase{ID: "t1"}
	result := &EvalResult{
		FinalScore:  0.3,
		Error:       "permission denied",
		ReplanCount: 0,
	}
	cat := c.Classify(context.Background(), task, result)
	if cat != FailErrorNoRecovery {
		t.Errorf("expected error_no_recovery, got %s", cat)
	}
}

func TestClassifyByRules_PlanningError(t *testing.T) {
	c := NewFailureClassifier(nil, 0)
	task := TaskCase{ID: "t1", Complexity: "complex", Dimension: DimPlanning}
	result := &EvalResult{FinalScore: 0.3}
	cat := c.Classify(context.Background(), task, result)
	if cat != FailPlanningError {
		t.Errorf("expected planning_error, got %s", cat)
	}
}

func TestClassifyByRules_SuccessReturnsEmpty(t *testing.T) {
	c := NewFailureClassifier(nil, 0)
	task := TaskCase{ID: "t1"}
	result := &EvalResult{Success: true, FinalScore: 0.9}
	cat := c.Classify(context.Background(), task, result)
	if cat != "" {
		t.Errorf("expected empty for success, got %s", cat)
	}
}

func TestClassifyAll(t *testing.T) {
	c := NewFailureClassifier(nil, 1*time.Minute)
	tasks := []TaskCase{
		{ID: "t1"},
		{ID: "t2"},
	}
	results := []EvalResult{
		{TaskID: "t1", Success: true, FinalScore: 0.9},
		{TaskID: "t2", FinalScore: 0.3, Duration: 2 * time.Minute},
	}
	classified := c.ClassifyAll(context.Background(), tasks, results)
	if classified[0].FailureCategory != "" {
		t.Errorf("t1 should not be classified, got %s", classified[0].FailureCategory)
	}
	if classified[1].FailureCategory != string(FailTimeout) {
		t.Errorf("t2 expected timeout, got %s", classified[1].FailureCategory)
	}
}

func TestAllFailureCategories(t *testing.T) {
	cats := AllFailureCategories()
	if len(cats) != 11 {
		t.Errorf("expected 11 categories, got %d", len(cats))
	}
}
