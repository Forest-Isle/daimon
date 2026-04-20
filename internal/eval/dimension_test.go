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

func TestAggregateDimensions_Basic(t *testing.T) {
	results := []EvalResult{
		{TaskID: "t1", Dimension: DimPlanning, Success: true, FinalScore: 0.9, ReplanCount: 1},
		{TaskID: "t2", Dimension: DimPlanning, Success: false, FinalScore: 0.4, ReplanCount: 3, FailureCategory: "planning_error"},
		{TaskID: "t3", Dimension: DimErrorRecovery, Success: true, FinalScore: 0.85, ReplanCount: 0},
	}
	report := AggregateDimensions(results)
	if report == nil {
		t.Fatal("report is nil")
	}
	if len(report.Dimensions) < 2 {
		t.Fatalf("expected at least 2 dimensions, got %d", len(report.Dimensions))
	}
	if report.FailureDistribution[FailPlanningError] != 1 {
		t.Errorf("expected 1 planning_error, got %d", report.FailureDistribution[FailPlanningError])
	}
}

func TestAggregateDimensions_WeakestStrongest(t *testing.T) {
	results := []EvalResult{
		{TaskID: "t1", Dimension: DimPlanning, FinalScore: 0.3},
		{TaskID: "t2", Dimension: DimPlanning, FinalScore: 0.4},
		{TaskID: "t3", Dimension: DimTaskExecution, FinalScore: 0.95},
		{TaskID: "t4", Dimension: DimTaskExecution, FinalScore: 0.9},
	}
	report := AggregateDimensions(results)
	if len(report.Weakest) == 0 {
		t.Error("expected at least one weak dimension")
	}
	if len(report.Strongest) == 0 {
		t.Error("expected at least one strong dimension")
	}
	if report.Weakest[0].Dimension != DimPlanning {
		t.Errorf("expected planning as weakest, got %s", report.Weakest[0].Dimension)
	}
}

func TestAggregateDimensions_Empty(t *testing.T) {
	report := AggregateDimensions(nil)
	if report == nil {
		t.Fatal("report is nil")
	}
	if len(report.Dimensions) != 0 {
		t.Errorf("expected 0 dimensions, got %d", len(report.Dimensions))
	}
}
