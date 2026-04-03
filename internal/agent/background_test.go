package agent

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestBackgroundManager_SpawnAndWait(t *testing.T) {
	bm := NewBackgroundManager()

	spec := &AgentSpec{Name: "test-bg", Description: "test"}
	runner := func(ctx context.Context) (*AgentResult, error) {
		time.Sleep(50 * time.Millisecond)
		return &AgentResult{AgentName: "test-bg", Output: "done"}, nil
	}

	agentID := bm.Spawn(context.Background(), spec, runner)
	if agentID == "" {
		t.Fatal("expected non-empty agent ID")
	}

	// Should be running initially
	result, done := bm.GetResult(agentID)
	if done {
		t.Error("expected agent to still be running")
	}
	if result != nil {
		t.Error("expected nil result while running")
	}

	// Wait for completion
	result, err := bm.Wait(context.Background(), agentID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Output != "done" {
		t.Errorf("expected output 'done', got %q", result.Output)
	}

	// Should be completed now
	result, done = bm.GetResult(agentID)
	if !done {
		t.Error("expected agent to be done")
	}
	if result.Output != "done" {
		t.Errorf("expected output 'done', got %q", result.Output)
	}
}

func TestBackgroundManager_SpawnFailure(t *testing.T) {
	bm := NewBackgroundManager()

	spec := &AgentSpec{Name: "fail-bg", Description: "test"}
	runner := func(ctx context.Context) (*AgentResult, error) {
		return nil, fmt.Errorf("agent failed")
	}

	agentID := bm.Spawn(context.Background(), spec, runner)

	result, err := bm.Wait(context.Background(), agentID)
	if err != nil {
		t.Fatalf("unexpected wait error: %v", err)
	}
	if result.Error == nil {
		t.Error("expected error in result")
	}
}

func TestBackgroundManager_Cancel(t *testing.T) {
	bm := NewBackgroundManager()

	spec := &AgentSpec{Name: "cancel-bg", Description: "test"}
	var cancelled atomic.Bool
	runner := func(ctx context.Context) (*AgentResult, error) {
		select {
		case <-ctx.Done():
			cancelled.Store(true)
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			return &AgentResult{Output: "should not reach"}, nil
		}
	}

	agentID := bm.Spawn(context.Background(), spec, runner)
	time.Sleep(20 * time.Millisecond) // let goroutine start

	if err := bm.Cancel(agentID); err != nil {
		t.Fatalf("cancel error: %v", err)
	}

	result, err := bm.Wait(context.Background(), agentID)
	if err != nil {
		t.Fatalf("wait error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
	// Should be cancelled
	if !cancelled.Load() {
		t.Error("expected runner to observe cancellation")
	}
}

func TestBackgroundManager_List(t *testing.T) {
	bm := NewBackgroundManager()

	spec1 := &AgentSpec{Name: "list1", Description: "test"}
	spec2 := &AgentSpec{Name: "list2", Description: "test"}

	bm.Spawn(context.Background(), spec1, func(ctx context.Context) (*AgentResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	bm.Spawn(context.Background(), spec2, func(ctx context.Context) (*AgentResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	statuses := bm.List()
	if len(statuses) != 2 {
		t.Errorf("expected 2 statuses, got %d", len(statuses))
	}
}

func TestBackgroundManager_Cleanup(t *testing.T) {
	bm := NewBackgroundManager()

	spec := &AgentSpec{Name: "cleanup", Description: "test"}
	agentID := bm.Spawn(context.Background(), spec, func(ctx context.Context) (*AgentResult, error) {
		return &AgentResult{Output: "fast"}, nil
	})

	// Wait for completion
	if _, err := bm.Wait(context.Background(), agentID); err != nil {
		t.Fatalf("wait: %v", err)
	}

	removed := bm.Cleanup()
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}

	statuses := bm.List()
	if len(statuses) != 0 {
		t.Errorf("expected 0 statuses after cleanup, got %d", len(statuses))
	}
}

func TestBackgroundManager_Notifications(t *testing.T) {
	bm := NewBackgroundManager()

	spec := &AgentSpec{Name: "notify", Description: "test"}
	bm.Spawn(context.Background(), spec, func(ctx context.Context) (*AgentResult, error) {
		return &AgentResult{Output: "ok"}, nil
	})

	// Should receive running + completed notifications
	var statuses []AgentStatus
	timeout := time.After(2 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case s := <-bm.NotifyCh():
			statuses = append(statuses, s)
		case <-timeout:
			t.Fatalf("timeout waiting for notification %d", i)
		}
	}

	if len(statuses) < 2 {
		t.Fatalf("expected at least 2 notifications, got %d", len(statuses))
	}
	if statuses[0].State != StateRunning {
		t.Errorf("first notification should be running, got %s", statuses[0].State)
	}
	if statuses[1].State != StateCompleted {
		t.Errorf("second notification should be completed, got %s", statuses[1].State)
	}
}

func TestBackgroundManager_WaitUnknown(t *testing.T) {
	bm := NewBackgroundManager()

	_, err := bm.Wait(context.Background(), "unknown-id")
	if err == nil {
		t.Error("expected error for unknown agent ID")
	}
}
