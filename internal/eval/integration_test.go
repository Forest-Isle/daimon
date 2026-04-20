package eval

import (
	"context"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
)

type judgeMockProvider struct {
	response string
}

func (m *judgeMockProvider) Complete(_ context.Context, req agent.CompletionRequest) (*agent.CompletionResponse, error) {
	return &agent.CompletionResponse{
		Text:       m.response,
		StopReason: agent.StopEndTurn,
	}, nil
}

func (m *judgeMockProvider) Stream(_ context.Context, req agent.CompletionRequest) (agent.StreamIterator, error) {
	return nil, nil
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

func TestFullPipeline_DryRun_WithVerification(t *testing.T) {
	exitCode := 0
	tasks := []TaskCase{
		{
			ID:           "verify-pass",
			Goal:         "echo hello world",
			Complexity:   "simple",
			Dimension:    DimTaskExecution,
			VerifyMethod: VerifyDeterministic,
			Reference: &Reference{
				MustContain: []string{"hello"},
			},
		},
		{
			ID:           "verify-fail",
			Goal:         "echo error",
			Complexity:   "simple",
			Dimension:    DimErrorRecovery,
			VerifyMethod: VerifyDeterministic,
			Reference: &Reference{
				MustNotContain: []string{"error"},
			},
		},
		{
			ID:           "judge-task",
			Goal:         "explain Go interfaces",
			Complexity:   "moderate",
			Dimension:    DimConversation,
			VerifyMethod: VerifyLLMJudge,
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "accuracy", Description: "correct?", Weight: 1.0},
				},
			},
		},
		{
			ID:           "hybrid-task",
			Goal:         "write hello to file",
			Complexity:   "moderate",
			Dimension:    DimPlanning,
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

	mockJudge := NewLLMJudge(&judgeMockProvider{
		response: `{"scores": {"accuracy": 0.8, "quality": 0.8}, "overall": 0.8, "reasoning": "ok", "weaknesses": []}`,
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

	// hybrid-task: both verify and judge
	r3 := suite.Results[3]
	if r3.VerifyResult == nil {
		t.Error("hybrid-task should have VerifyResult")
	}
	if r3.JudgeResult == nil {
		t.Error("hybrid-task should have JudgeResult")
	}

	// legacy-task: no new fields, FinalScore falls back to AssertionPassRate
	r4 := suite.Results[4]
	if r4.Dimension != DimTaskExecution {
		t.Errorf("legacy-task should default to task_execution, got %s", r4.Dimension)
	}
}
