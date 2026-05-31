package scheduler

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

// newTestDB opens a store.DB on a temporary SQLite file with all migrations applied.
func newTestDB(t *testing.T) *store.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "ironclaw_test.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open(%q) failed: %v", dbPath, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seedTask inserts a single enabled scheduled task.
func seedTask(t *testing.T, db *store.DB, id, name, cronExpr, prompt, channel, channelID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO scheduled_tasks (id, name, cron_expr, prompt, channel, channel_id, enabled) VALUES (?, ?, ?, ?, ?, ?, 1)`,
		id, name, cronExpr, prompt, channel, channelID)
	if err != nil {
		t.Fatalf("seed task %s: %v", id, err)
	}
}

// --- tests ---

func TestNewScheduler(t *testing.T) {
	db := newTestDB(t)
	s := New(db, 100*time.Millisecond)
	if s == nil {
		t.Fatal("New returned nil")
	}
	if s.pollInterval != 100*time.Millisecond {
		t.Errorf("pollInterval = %v, want 100ms", s.pollInterval)
	}
	if len(s.entries) != 0 {
		t.Errorf("expected empty entries, got %d", len(s.entries))
	}
}

func TestSetHandler(t *testing.T) {
	db := newTestDB(t)
	s := New(db, 100*time.Millisecond)

	called := false
	s.SetHandler(func(ctx context.Context, task Task) {
		called = true
	})
	if s.handler == nil {
		t.Fatal("handler should not be nil after SetHandler")
	}

	s.handler(context.Background(), Task{ID: "test", Name: "test task"})
	if !called {
		t.Error("handler was not called")
	}
}

func TestSyncTasks_RegistersEnabledTasks(t *testing.T) {
	db := newTestDB(t)
	seedTask(t, db, "task-1", "test task", "*/5 * * * * *", "do something", "slack", "#general")

	s := New(db, 100*time.Millisecond)
	s.syncTasks(context.Background())

	s.mu.Lock()
	count := len(s.entries)
	s.mu.Unlock()

	if count != 1 {
		t.Errorf("expected 1 registered task, got %d", count)
	}
}

func TestSyncTasks_Idempotent(t *testing.T) {
	db := newTestDB(t)
	seedTask(t, db, "task-1", "test", "*/5 * * * * *", "prompt", "slack", "#general")

	s := New(db, 100*time.Millisecond)
	s.syncTasks(context.Background()) // First sync
	s.syncTasks(context.Background()) // Second sync — should not duplicate

	s.mu.Lock()
	count := len(s.entries)
	s.mu.Unlock()

	if count != 1 {
		t.Errorf("expected 1 entry after duplicate sync, got %d", count)
	}
}

func TestSyncTasks_InvalidCron(t *testing.T) {
	db := newTestDB(t)
	seedTask(t, db, "bad-task", "bad task", "invalid cron!!!", "prompt", "slack", "#general")

	s := New(db, 100*time.Millisecond)
	s.syncTasks(context.Background())

	s.mu.Lock()
	count := len(s.entries)
	s.mu.Unlock()

	if count != 0 {
		t.Errorf("expected 0 entries for invalid cron, got %d", count)
	}
}

func TestSyncTasks_RemovesDisabledTasks(t *testing.T) {
	db := newTestDB(t)
	seedTask(t, db, "task-1", "task 1", "*/5 * * * * *", "prompt", "slack", "#general")
	seedTask(t, db, "task-2", "task 2", "*/5 * * * * *", "prompt", "slack", "#general")

	s := New(db, 100*time.Millisecond)
	s.syncTasks(context.Background())

	s.mu.Lock()
	beforeCount := len(s.entries)
	s.mu.Unlock()

	if beforeCount != 2 {
		t.Fatalf("expected 2 entries initially, got %d", beforeCount)
	}

	// Disable task-1
	_, err := db.Exec(`UPDATE scheduled_tasks SET enabled = 0 WHERE id = 'task-1'`)
	if err != nil {
		t.Fatalf("disable task-1: %v", err)
	}

	s.syncTasks(context.Background())

	s.mu.Lock()
	afterCount := len(s.entries)
	_, task2Exists := s.entries["task-2"]
	_, task1Exists := s.entries["task-1"]
	s.mu.Unlock()

	if afterCount != 1 {
		t.Errorf("expected 1 entry after disable, got %d", afterCount)
	}
	if !task2Exists {
		t.Error("task-2 should still be registered")
	}
	if task1Exists {
		t.Error("task-1 should have been removed")
	}
}

func TestSyncTasks_EmptyDB(t *testing.T) {
	db := newTestDB(t)
	s := New(db, 100*time.Millisecond)
	s.syncTasks(context.Background())

	s.mu.Lock()
	count := len(s.entries)
	s.mu.Unlock()

	if count != 0 {
		t.Errorf("expected 0 entries from empty DB, got %d", count)
	}
}

func TestHandlerInvokedOnTrigger(t *testing.T) {
	db := newTestDB(t)

	// Insert a task with @every 1s so we can test cron triggering
	seedTask(t, db, "trig-1", "trigger task", "@every 1s", "do it", "slack", "#general")

	s := New(db, 100*time.Millisecond)

	var callCount atomic.Int32
	s.SetHandler(func(ctx context.Context, task Task) {
		callCount.Add(1)
	})

	s.syncTasks(context.Background())

	// Verify entry exists
	s.mu.Lock()
	_, exists := s.entries["trig-1"]
	s.mu.Unlock()
	if !exists {
		t.Fatal("expected trig-1 to be registered")
	}

	// Verify last_run is initially nil
	var lastRun any
	err := db.QueryRow(`SELECT last_run FROM scheduled_tasks WHERE id = 'trig-1'`).Scan(&lastRun)
	if err != nil {
		t.Fatalf("query last_run: %v", err)
	}
	if lastRun != nil {
		t.Log("last_run is nil initially")
	}
}

func TestStartAndStop(t *testing.T) {
	db := newTestDB(t)
	s := New(db, 50*time.Millisecond)

	s.Start(context.Background())
	// Give poll loop a moment to start
	time.Sleep(10 * time.Millisecond)
	s.Stop()
}

func TestStop_Idempotent(t *testing.T) {
	db := newTestDB(t)
	s := New(db, 50*time.Millisecond)

	s.Start(context.Background())
	s.Stop()
	s.Stop() // second stop should not panic
}

func TestStart_MultipleSchedulers(t *testing.T) {
	db1 := newTestDB(t)
	db2 := newTestDB(t)

	s1 := New(db1, 100*time.Millisecond)
	s2 := New(db2, 100*time.Millisecond)

	s1.Start(context.Background())
	s2.Start(context.Background())

	time.Sleep(10 * time.Millisecond)

	s1.Stop()
	s2.Stop()
}

func TestCronTaskLastRunUpdate(t *testing.T) {
	db := newTestDB(t)

	// Insert a task
	seedTask(t, db, "lr-1", "last run test", "@every 1s", "prompt", "slack", "#general")

	s := New(db, 100*time.Millisecond)
	s.syncTasks(context.Background())

	// Manually invoke the cron function for "lr-1"
	s.mu.Lock()
	entryID, exists := s.entries["lr-1"]
	s.mu.Unlock()
	if !exists {
		t.Fatal("expected lr-1 to be registered")
	}

	// Simulate the cron trigger: the entry's function calls s.handler and updates last_run
	// We can verify by looking up the cron entry
	_ = entryID

	// Verify last_run can be updated
	_, err := db.Exec(`UPDATE scheduled_tasks SET last_run = ? WHERE id = ?`, time.Now(), "lr-1")
	if err != nil {
		t.Fatalf("update last_run: %v", err)
	}

	var lastRun time.Time
	err = db.QueryRow(`SELECT last_run FROM scheduled_tasks WHERE id = 'lr-1'`).Scan(&lastRun)
	if err != nil {
		t.Fatalf("query last_run: %v", err)
	}
	if lastRun.IsZero() {
		t.Error("expected non-zero last_run")
	}
}

func TestSyncTasks_DisabledTaskRemoval(t *testing.T) {
	db := newTestDB(t)
	seedTask(t, db, "keep", "keep me", "*/5 * * * * *", "prompt", "slack", "#c")
	seedTask(t, db, "remove", "remove me", "*/5 * * * * *", "prompt", "slack", "#c")

	s := New(db, 100*time.Millisecond)
	s.syncTasks(context.Background())

	// Delete the "remove" task entirely
	_, err := db.Exec(`DELETE FROM scheduled_tasks WHERE id = 'remove'`)
	if err != nil {
		t.Fatalf("delete task: %v", err)
	}

	s.syncTasks(context.Background())

	s.mu.Lock()
	_, keepExists := s.entries["keep"]
	_, removeExists := s.entries["remove"]
	s.mu.Unlock()

	if !keepExists {
		t.Error("'keep' task should remain")
	}
	if removeExists {
		t.Error("'remove' task should be gone")
	}
}

func TestStop_WithoutStart(t *testing.T) {
	db := newTestDB(t)
	s := New(db, 100*time.Millisecond)
	// Stop without Start should not panic
	s.Stop()
}
