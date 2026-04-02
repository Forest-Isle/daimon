package agent

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
)

func TestAgentHookRunner_RunOnStart(t *testing.T) {
	var callCount atomic.Int32
	hooks := AgentHooks{
		OnStart: []AgentHookFunc{
			func(ctx context.Context, hctx *AgentHookContext) error {
				callCount.Add(1)
				return nil
			},
			func(ctx context.Context, hctx *AgentHookContext) error {
				callCount.Add(1)
				return nil
			},
		},
	}

	runner := NewAgentHookRunner(hooks)
	runner.RunOnStart(context.Background(), &AgentHookContext{
		AgentID:   "test-1",
		AgentName: "test-agent",
	})

	if callCount.Load() != 2 {
		t.Errorf("expected 2 hook calls, got %d", callCount.Load())
	}
}

func TestAgentHookRunner_HookFailureDoesNotBlock(t *testing.T) {
	var secondCalled atomic.Bool
	hooks := AgentHooks{
		OnStart: []AgentHookFunc{
			func(ctx context.Context, hctx *AgentHookContext) error {
				return fmt.Errorf("hook 1 failed")
			},
			func(ctx context.Context, hctx *AgentHookContext) error {
				secondCalled.Store(true)
				return nil
			},
		},
	}

	runner := NewAgentHookRunner(hooks)
	runner.RunOnStart(context.Background(), &AgentHookContext{AgentName: "test"})

	if !secondCalled.Load() {
		t.Error("second hook should still run after first hook fails")
	}
}

func TestAgentHookRunner_AllPhases(t *testing.T) {
	var phases []string
	makeHook := func(phase string) AgentHookFunc {
		return func(ctx context.Context, hctx *AgentHookContext) error {
			phases = append(phases, phase)
			return nil
		}
	}

	hooks := AgentHooks{
		OnStart:    []AgentHookFunc{makeHook("start")},
		OnComplete: []AgentHookFunc{makeHook("complete")},
		OnError:    []AgentHookFunc{makeHook("error")},
		OnTimeout:  []AgentHookFunc{makeHook("timeout")},
		OnToolCall: []AgentHookFunc{makeHook("toolcall")},
	}

	runner := NewAgentHookRunner(hooks)
	hctx := &AgentHookContext{AgentName: "test"}

	runner.RunOnStart(context.Background(), hctx)
	runner.RunOnComplete(context.Background(), hctx)
	runner.RunOnError(context.Background(), hctx)
	runner.RunOnTimeout(context.Background(), hctx)
	runner.RunOnToolCall(context.Background(), hctx)

	expected := []string{"start", "complete", "error", "timeout", "toolcall"}
	if len(phases) != len(expected) {
		t.Fatalf("expected %d phases, got %d", len(expected), len(phases))
	}
	for i, p := range expected {
		if phases[i] != p {
			t.Errorf("phase %d: expected %q, got %q", i, p, phases[i])
		}
	}
}

func TestAgentHookRunner_HasHooks(t *testing.T) {
	empty := NewAgentHookRunner(AgentHooks{})
	if empty.HasHooks() {
		t.Error("empty hooks should return false")
	}

	withStart := NewAgentHookRunner(AgentHooks{
		OnStart: []AgentHookFunc{func(ctx context.Context, hctx *AgentHookContext) error { return nil }},
	})
	if !withStart.HasHooks() {
		t.Error("hooks with OnStart should return true")
	}
}

func TestBuildAgentHooks_Log(t *testing.T) {
	cfg := AgentHookConfig{
		OnStart: []AgentHookEntry{
			{Type: "log", Message: "agent starting"},
		},
	}

	hooks := BuildAgentHooks(cfg)
	if len(hooks.OnStart) != 1 {
		t.Fatalf("expected 1 OnStart hook, got %d", len(hooks.OnStart))
	}

	// Log hook should not error
	err := hooks.OnStart[0](context.Background(), &AgentHookContext{AgentName: "test"})
	if err != nil {
		t.Errorf("log hook should not error: %v", err)
	}
}

func TestBuildAgentHooks_UnknownType(t *testing.T) {
	cfg := AgentHookConfig{
		OnStart: []AgentHookEntry{
			{Type: "unknown_type"},
		},
	}

	hooks := BuildAgentHooks(cfg)
	// Unknown types are silently skipped
	if len(hooks.OnStart) != 0 {
		t.Errorf("unknown hook type should be skipped, got %d hooks", len(hooks.OnStart))
	}
}

func TestBuildAgentHooks_Empty(t *testing.T) {
	hooks := BuildAgentHooks(AgentHookConfig{})
	runner := NewAgentHookRunner(hooks)
	if runner.HasHooks() {
		t.Error("empty config should produce no hooks")
	}
}

func TestAgentHookContext_Fields(t *testing.T) {
	hctx := &AgentHookContext{
		AgentID:   "id-1",
		AgentName: "agent-1",
		ParentID:  "parent-1",
		Task:      "do something",
		ToolName:  "bash",
	}

	if hctx.AgentID != "id-1" {
		t.Errorf("expected AgentID 'id-1', got %q", hctx.AgentID)
	}
	if hctx.ToolName != "bash" {
		t.Errorf("expected ToolName 'bash', got %q", hctx.ToolName)
	}
}
