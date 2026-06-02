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

	result, err := judge.Judge(context.Background(), task, "Go interfaces define method sets...", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Overall is recomputed from weights: 0.9*0.6 + 0.8*0.4 = 0.86
	if diff := result.Overall - 0.86; diff > 0.01 || diff < -0.01 {
		t.Errorf("Overall = %f, want ~0.86", result.Overall)
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

	result, err := judge.Judge(context.Background(), task, "some output", nil)
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
	result, err := judge.Judge(context.Background(), task, "output", nil)
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
		Goal:      "Explain channels in Go",
		Reference: &Reference{Answer: "Channels are typed conduits"},
		Rubric: &Rubric{
			Criteria: []JudgeCriterion{
				{Name: "accuracy", Description: "correct?", Weight: 1.0},
			},
		},
	}
	prompt := judge.buildPrompt(task, "My answer about channels", []string{"bash", "file_read"})
	if prompt == "" {
		t.Error("prompt should not be empty")
	}
}
