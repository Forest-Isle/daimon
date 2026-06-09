package agent

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestInProcessBackend_Execute(t *testing.T) {
	executor := func(ctx context.Context, cfg BackendConfig) (*AgentResult, error) {
		time.Sleep(10 * time.Millisecond)
		return &AgentResult{
			AgentName: cfg.Spec.Name,
			Output:    "result from " + cfg.Task,
		}, nil
	}

	be := NewInProcessBackend(executor)
	if !be.Available() {
		t.Error("in-process should always be available")
	}
	if be.Name() != "in_process" {
		t.Errorf("expected name 'in_process', got %q", be.Name())
	}

	spec := &AgentSpec{Name: "test-agent", Description: "test"}
	ch, err := be.Execute(context.Background(), BackendConfig{
		Spec: spec,
		Task: "do something",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := <-ch
	if result == nil {
		t.Fatal("expected result")
	}
	if result.Output != "result from do something" {
		t.Errorf("unexpected output: %q", result.Output)
	}
	if result.Duration == 0 {
		t.Error("expected non-zero duration")
	}
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}
}

func TestInProcessBackend_ExecuteError(t *testing.T) {
	executor := func(ctx context.Context, cfg BackendConfig) (*AgentResult, error) {
		return nil, fmt.Errorf("execution failed")
	}

	be := NewInProcessBackend(executor)
	spec := &AgentSpec{Name: "fail", Description: "test"}
	ch, err := be.Execute(context.Background(), BackendConfig{Spec: spec, Task: "fail"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := <-ch
	if result.Error == nil {
		t.Error("expected error in result")
	}
}

func TestInProcessBackend_NoExecutor(t *testing.T) {
	be := NewInProcessBackend(nil)
	_, err := be.Execute(context.Background(), BackendConfig{})
	if err == nil {
		t.Error("expected error for nil executor")
	}
}

func TestInProcessBackend_ContextCancellation(t *testing.T) {
	executor := func(ctx context.Context, cfg BackendConfig) (*AgentResult, error) {
		select {
		case <-ctx.Done():
			return &AgentResult{AgentName: "cancelled", Error: ctx.Err()}, nil
		case <-time.After(10 * time.Second):
			return &AgentResult{Output: "should not reach"}, nil
		}
	}

	be := NewInProcessBackend(executor)
	ctx, cancel := context.WithCancel(context.Background())

	spec := &AgentSpec{Name: "cancel-test", Description: "test"}
	ch, err := be.Execute(ctx, BackendConfig{Spec: spec, Task: "wait"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cancel()
	result := <-ch
	if result == nil {
		t.Fatal("expected result")
	}
	if result.Error == nil {
		t.Error("expected cancellation error")
	}
}

func TestBackendCleanup(t *testing.T) {
	be := NewInProcessBackend(nil)
	if err := be.Cleanup(); err != nil {
		t.Errorf("unexpected cleanup error: %v", err)
	}
}
