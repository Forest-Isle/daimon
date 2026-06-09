package agent

import (
	"context"
	"testing"
)

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

