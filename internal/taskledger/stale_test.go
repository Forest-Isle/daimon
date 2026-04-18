package taskledger

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestStaleDetector_DetectsAndFails(t *testing.T) {
	ledger := NewSQLiteTaskLedger(newTestDB(t))
	ctx := context.Background()

	oldTime := time.Now().Add(-10 * time.Minute)
	if err := ledger.Register(ctx, Task{
		ID:        "stale-1",
		Kind:      TaskKindSubAgent,
		State:     TaskStateRunning,
		Title:     "Will go stale",
		StartedAt: &oldTime,
		Heartbeat: &oldTime,
	}); err != nil {
		t.Fatal(err)
	}

	sd := NewStaleDetector(ledger, 1*time.Minute, 50*time.Millisecond, nil)
	sd.Start()
	defer sd.Stop()

	// Wait for at least one sweep cycle
	time.Sleep(200 * time.Millisecond)

	got, err := ledger.Get(ctx, "stale-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != TaskStateFailed {
		t.Errorf("State = %q, want %q", got.State, TaskStateFailed)
	}
	if got.Result != "stale heartbeat" {
		t.Errorf("Result = %q, want %q", got.Result, "stale heartbeat")
	}
}

func TestStaleDetector_CallbackInvoked(t *testing.T) {
	ledger := NewSQLiteTaskLedger(newTestDB(t))
	ctx := context.Background()

	oldTime := time.Now().Add(-10 * time.Minute)
	if err := ledger.Register(ctx, Task{
		ID:        "cb-task",
		Kind:      TaskKindUserRequest,
		State:     TaskStateRunning,
		Title:     "Callback test",
		StartedAt: &oldTime,
		Heartbeat: &oldTime,
	}); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var callbackTasks []Task
	cb := func(task Task) {
		mu.Lock()
		callbackTasks = append(callbackTasks, task)
		mu.Unlock()
	}

	sd := NewStaleDetector(ledger, 1*time.Minute, 50*time.Millisecond, cb)
	sd.Start()
	defer sd.Stop()

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(callbackTasks) == 0 {
		t.Fatal("expected callback to be invoked at least once")
	}
	if callbackTasks[0].ID != "cb-task" {
		t.Errorf("callback task ID = %q, want %q", callbackTasks[0].ID, "cb-task")
	}
	if callbackTasks[0].State != TaskStateFailed {
		t.Errorf("callback task State = %q, want %q", callbackTasks[0].State, TaskStateFailed)
	}
}

func TestStaleDetector_StopsGracefully(t *testing.T) {
	ledger := NewSQLiteTaskLedger(newTestDB(t))

	sd := NewStaleDetector(ledger, 1*time.Minute, 50*time.Millisecond, nil)
	sd.Start()

	// Stop should return promptly without blocking
	done := make(chan struct{})
	go func() {
		sd.Stop()
		close(done)
	}()

	select {
	case <-done:
		// OK — goroutine exited
	case <-time.After(2 * time.Second):
		t.Fatal("StaleDetector.Stop did not return within 2s")
	}
}
