package taskledger

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestTeamCoordinator_WorkerClaimsAndCompletes(t *testing.T) {
	ledger := NewSQLiteTaskLedger(newTestDB(t))
	tc := NewTeamCoordinator(ledger, 2)

	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		err := tc.AddTask(ctx, Task{
			ID:    fmt.Sprintf("task-%d", i),
			Title: fmt.Sprintf("Task %d", i),
		})
		if err != nil {
			t.Fatalf("AddTask %d: %v", i, err)
		}
	}

	var calls atomic.Int32
	tc.SetExecutor(func(_ context.Context, task Task) (string, error) {
		calls.Add(1)
		return "done: " + task.ID, nil
	})

	result, err := tc.RunWithExecutor(ctx)
	if err != nil {
		t.Fatalf("RunWithExecutor: %v", err)
	}

	if result.TasksCompleted != 3 {
		t.Errorf("TasksCompleted = %d, want 3", result.TasksCompleted)
	}
	if result.TasksFailed != 0 {
		t.Errorf("TasksFailed = %d, want 0", result.TasksFailed)
	}
	if calls.Load() != 3 {
		t.Errorf("executor called %d times, want 3", calls.Load())
	}

	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("task-%d", i)
		task, err := ledger.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if task.State != TaskStateCompleted {
			t.Errorf("%s state = %q, want %q", id, task.State, TaskStateCompleted)
		}
		if task.Result != "done: "+id {
			t.Errorf("%s result = %q, want %q", id, task.Result, "done: "+id)
		}
	}
}

func TestTeamCoordinator_Dependencies(t *testing.T) {
	ledger := NewSQLiteTaskLedger(newTestDB(t))
	tc := NewTeamCoordinator(ledger, 2)

	ctx := context.Background()

	err := tc.AddTask(ctx, Task{
		ID:    "task-a",
		Title: "Task A",
	})
	if err != nil {
		t.Fatalf("AddTask A: %v", err)
	}

	err = tc.AddTask(ctx, Task{
		ID:        "task-b",
		Title:     "Task B",
		DependsOn: []string{"task-a"},
	})
	if err != nil {
		t.Fatalf("AddTask B: %v", err)
	}

	var order []string
	var orderMu = make(chan struct{}, 1)
	appendOrder := func(id string) {
		orderMu <- struct{}{}
		order = append(order, id)
		<-orderMu
	}

	tc.SetExecutor(func(_ context.Context, task Task) (string, error) {
		if task.ID == "task-a" {
			time.Sleep(20 * time.Millisecond)
		}
		appendOrder(task.ID)
		return "ok", nil
	})

	result, err := tc.RunWithExecutor(ctx)
	if err != nil {
		t.Fatalf("RunWithExecutor: %v", err)
	}

	if result.TasksCompleted != 2 {
		t.Errorf("TasksCompleted = %d, want 2", result.TasksCompleted)
	}

	if len(order) != 2 {
		t.Fatalf("order length = %d, want 2", len(order))
	}
	if order[0] != "task-a" || order[1] != "task-b" {
		t.Errorf("execution order = %v, want [task-a, task-b]", order)
	}
}

func TestTeamCoordinator_FailedDependency(t *testing.T) {
	ledger := NewSQLiteTaskLedger(newTestDB(t))
	tc := NewTeamCoordinator(ledger, 2)

	ctx := context.Background()

	err := tc.AddTask(ctx, Task{
		ID:    "task-a",
		Title: "Task A (will fail)",
	})
	if err != nil {
		t.Fatalf("AddTask A: %v", err)
	}

	err = tc.AddTask(ctx, Task{
		ID:        "task-b",
		Title:     "Task B (depends on A)",
		DependsOn: []string{"task-a"},
	})
	if err != nil {
		t.Fatalf("AddTask B: %v", err)
	}

	tc.SetExecutor(func(_ context.Context, task Task) (string, error) {
		if task.ID == "task-a" {
			return "", fmt.Errorf("deliberate failure")
		}
		return "ok", nil
	})

	result, err := tc.RunWithExecutor(ctx)
	if err != nil {
		t.Fatalf("RunWithExecutor: %v", err)
	}

	if result.TasksFailed != 1 {
		t.Errorf("TasksFailed = %d, want 1", result.TasksFailed)
	}
	if result.TasksCancelled != 1 {
		t.Errorf("TasksCancelled = %d, want 1", result.TasksCancelled)
	}

	taskA, _ := ledger.Get(ctx, "task-a")
	if taskA.State != TaskStateFailed {
		t.Errorf("task-a state = %q, want %q", taskA.State, TaskStateFailed)
	}

	taskB, _ := ledger.Get(ctx, "task-b")
	if taskB.State != TaskStateCancelled {
		t.Errorf("task-b state = %q, want %q", taskB.State, TaskStateCancelled)
	}
}
