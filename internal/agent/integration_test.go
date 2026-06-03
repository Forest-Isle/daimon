package agent

import (
	"context"
	"testing"
	"time"
)

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

func TestAgentContext_RoundTrip(t *testing.T) {
	ctx := context.Background()

	if a := AgentFromContext(ctx); a != nil {
		t.Error("expected nil agent from empty context")
	}

	deps := AgentDeps{Core: CoreDeps{AgentID: "test-123"}}.WithDefaults()
	agent := NewAgent(&deps, &SimpleLoop{}, NewEventBus())
	ctx = AgentToContext(ctx, agent)

	a := AgentFromContext(ctx)
	if a == nil {
		t.Fatal("expected non-nil agent")
	}
	if a.AgentID() != "test-123" {
		t.Errorf("expected agent ID 'test-123', got %q", a.AgentID())
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
		time.Sleep(5 * time.Millisecond)
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
