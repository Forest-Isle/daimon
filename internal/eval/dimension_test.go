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
