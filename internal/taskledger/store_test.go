package taskledger

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/store"
	_ "github.com/mattn/go-sqlite3"
)

const migrationSQL = `
CREATE TABLE IF NOT EXISTS task_ledger (
    id TEXT PRIMARY KEY,
    parent_id TEXT DEFAULT '',
    kind TEXT NOT NULL DEFAULT 'user_request',
    state TEXT NOT NULL DEFAULT 'pending',
    title TEXT NOT NULL DEFAULT '',
    description TEXT DEFAULT '',
    assignee TEXT DEFAULT '',
    depends_on TEXT DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at DATETIME,
    completed_at DATETIME,
    heartbeat DATETIME,
    result TEXT DEFAULT '',
    metadata TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_task_ledger_state ON task_ledger(state);
CREATE INDEX IF NOT EXISTS idx_task_ledger_parent ON task_ledger(parent_id);
CREATE INDEX IF NOT EXISTS idx_task_ledger_kind ON task_ledger(kind);
`

func newTestDB(t *testing.T) *store.DB {
	t.Helper()
	raw, err := sql.Open("sqlite3", ":memory:?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec(migrationSQL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return &store.DB{DB: raw}
}

func TestSQLiteTaskLedger_Register_Get(t *testing.T) {
	ledger := NewSQLiteTaskLedger(newTestDB(t))
	ctx := context.Background()

	task := Task{
		ID:          "task-001",
		ParentID:    "",
		Kind:        TaskKindUserRequest,
		State:       TaskStatePending,
		Title:       "Test Task",
		Description: "A test task description",
		Assignee:    "agent-1",
		DependsOn:   []string{"dep-1", "dep-2"},
		Metadata:    map[string]string{"priority": "high", "source": "test"},
	}

	if err := ledger.Register(ctx, task); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, err := ledger.Get(ctx, "task-001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ID != "task-001" {
		t.Errorf("ID = %q, want %q", got.ID, "task-001")
	}
	if got.Kind != TaskKindUserRequest {
		t.Errorf("Kind = %q, want %q", got.Kind, TaskKindUserRequest)
	}
	if got.State != TaskStatePending {
		t.Errorf("State = %q, want %q", got.State, TaskStatePending)
	}
	if got.Title != "Test Task" {
		t.Errorf("Title = %q, want %q", got.Title, "Test Task")
	}
	if got.Description != "A test task description" {
		t.Errorf("Description = %q, want %q", got.Description, "A test task description")
	}
	if got.Assignee != "agent-1" {
		t.Errorf("Assignee = %q, want %q", got.Assignee, "agent-1")
	}
	if len(got.DependsOn) != 2 || got.DependsOn[0] != "dep-1" || got.DependsOn[1] != "dep-2" {
		t.Errorf("DependsOn = %v, want [dep-1, dep-2]", got.DependsOn)
	}
	if got.Metadata["priority"] != "high" || got.Metadata["source"] != "test" {
		t.Errorf("Metadata = %v, want map[priority:high source:test]", got.Metadata)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}

	// Get non-existent task
	_, err = ledger.Get(ctx, "no-such-task")
	if err == nil {
		t.Error("Get non-existent task should return error")
	}
}

func TestSQLiteTaskLedger_ClaimNext_Atomic(t *testing.T) {
	ledger := NewSQLiteTaskLedger(newTestDB(t))
	ctx := context.Background()

	for i, id := range []string{"task-a", "task-b", "task-c"} {
		if err := ledger.Register(ctx, Task{
			ID:        id,
			Kind:      TaskKindSubAgent,
			State:     TaskStatePending,
			Title:     "Task " + id,
			CreatedAt: time.Now().Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("Register %s: %v", id, err)
		}
	}

	claimed, err := ledger.ClaimNext(ctx, TaskKindSubAgent, "worker-1")
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNext returned nil")
	}
	if claimed.ID != "task-a" {
		t.Errorf("claimed ID = %q, want %q", claimed.ID, "task-a")
	}
	if claimed.State != TaskStateRunning {
		t.Errorf("claimed State = %q, want %q", claimed.State, TaskStateRunning)
	}
	if claimed.Assignee != "worker-1" {
		t.Errorf("claimed Assignee = %q, want %q", claimed.Assignee, "worker-1")
	}
	if claimed.StartedAt == nil {
		t.Error("claimed StartedAt should be set")
	}

	// Second claim should get task-b
	claimed2, err := ledger.ClaimNext(ctx, TaskKindSubAgent, "worker-2")
	if err != nil {
		t.Fatalf("ClaimNext 2: %v", err)
	}
	if claimed2.ID != "task-b" {
		t.Errorf("second claim ID = %q, want %q", claimed2.ID, "task-b")
	}

	// Claim with a different kind should return nil
	noTask, err := ledger.ClaimNext(ctx, TaskKindScheduled, "worker-3")
	if err != nil {
		t.Fatalf("ClaimNext wrong kind: %v", err)
	}
	if noTask != nil {
		t.Errorf("expected nil for wrong kind, got %v", noTask)
	}
}

func TestSQLiteTaskLedger_Cancel_Cascades(t *testing.T) {
	ledger := NewSQLiteTaskLedger(newTestDB(t))
	ctx := context.Background()

	// parent → child1, child2; child2 is already completed
	tasks := []Task{
		{ID: "parent", Kind: TaskKindUserRequest, State: TaskStateRunning, Title: "Parent"},
		{ID: "child-1", ParentID: "parent", Kind: TaskKindSubAgent, State: TaskStatePending, Title: "Child 1"},
		{ID: "child-2", ParentID: "parent", Kind: TaskKindSubAgent, State: TaskStateCompleted, Title: "Child 2"},
		{ID: "grandchild-1", ParentID: "child-1", Kind: TaskKindSubAgent, State: TaskStatePending, Title: "Grandchild 1"},
	}
	for _, tk := range tasks {
		if err := ledger.Register(ctx, tk); err != nil {
			t.Fatalf("Register %s: %v", tk.ID, err)
		}
	}

	if err := ledger.Cancel(ctx, "parent", "user cancelled"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	for _, tc := range []struct {
		id    string
		state TaskState
	}{
		{"parent", TaskStateCancelled},
		{"child-1", TaskStateCancelled},
		{"child-2", TaskStateCompleted}, // already completed, should not change
		{"grandchild-1", TaskStateCancelled},
	} {
		got, err := ledger.Get(ctx, tc.id)
		if err != nil {
			t.Fatalf("Get %s: %v", tc.id, err)
		}
		if got.State != tc.state {
			t.Errorf("%s: State = %q, want %q", tc.id, got.State, tc.state)
		}
	}

	// Verify the reason is set on cancelled tasks
	parent, _ := ledger.Get(ctx, "parent")
	if parent.Result != "user cancelled" {
		t.Errorf("parent Result = %q, want %q", parent.Result, "user cancelled")
	}
}

func TestSQLiteTaskLedger_GetTree(t *testing.T) {
	ledger := NewSQLiteTaskLedger(newTestDB(t))
	ctx := context.Background()

	tasks := []Task{
		{ID: "root", Kind: TaskKindUserRequest, State: TaskStatePending, Title: "Root"},
		{ID: "child-a", ParentID: "root", Kind: TaskKindSubAgent, State: TaskStatePending, Title: "Child A"},
		{ID: "child-b", ParentID: "root", Kind: TaskKindSubAgent, State: TaskStatePending, Title: "Child B"},
		{ID: "unrelated", Kind: TaskKindUserRequest, State: TaskStatePending, Title: "Unrelated"},
	}
	for _, tk := range tasks {
		if err := ledger.Register(ctx, tk); err != nil {
			t.Fatalf("Register %s: %v", tk.ID, err)
		}
	}

	tree, err := ledger.GetTree(ctx, "root")
	if err != nil {
		t.Fatalf("GetTree: %v", err)
	}

	if len(tree) != 3 {
		t.Fatalf("GetTree returned %d tasks, want 3", len(tree))
	}

	ids := make(map[string]bool)
	for _, tk := range tree {
		ids[tk.ID] = true
	}
	for _, want := range []string{"root", "child-a", "child-b"} {
		if !ids[want] {
			t.Errorf("GetTree missing task %q", want)
		}
	}
	if ids["unrelated"] {
		t.Error("GetTree should not include unrelated task")
	}
}

func TestSQLiteTaskLedger_DetectStale(t *testing.T) {
	ledger := NewSQLiteTaskLedger(newTestDB(t))
	ctx := context.Background()

	oldTime := time.Now().Add(-10 * time.Minute)
	recentTime := time.Now()

	tasks := []Task{
		{ID: "stale-task", Kind: TaskKindSubAgent, State: TaskStateRunning, Title: "Stale", StartedAt: &oldTime, Heartbeat: &oldTime},
		{ID: "fresh-task", Kind: TaskKindSubAgent, State: TaskStateRunning, Title: "Fresh", StartedAt: &recentTime, Heartbeat: &recentTime},
		{ID: "pending-task", Kind: TaskKindSubAgent, State: TaskStatePending, Title: "Pending"},
	}
	for _, tk := range tasks {
		if err := ledger.Register(ctx, tk); err != nil {
			t.Fatalf("Register %s: %v", tk.ID, err)
		}
	}

	stale, err := ledger.DetectStale(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("DetectStale: %v", err)
	}

	if len(stale) != 1 {
		t.Fatalf("DetectStale returned %d tasks, want 1", len(stale))
	}
	if stale[0].ID != "stale-task" {
		t.Errorf("stale task ID = %q, want %q", stale[0].ID, "stale-task")
	}
}

func TestSQLiteTaskLedger_List_Filter(t *testing.T) {
	ledger := NewSQLiteTaskLedger(newTestDB(t))
	ctx := context.Background()

	tasks := []Task{
		{ID: "t1", Kind: TaskKindUserRequest, State: TaskStatePending, Title: "T1"},
		{ID: "t2", Kind: TaskKindSubAgent, State: TaskStatePending, Title: "T2"},
		{ID: "t3", Kind: TaskKindUserRequest, State: TaskStateRunning, Title: "T3"},
		{ID: "t4", Kind: TaskKindSubAgent, State: TaskStateCompleted, Title: "T4", ParentID: "t1"},
	}
	for _, tk := range tasks {
		if err := ledger.Register(ctx, tk); err != nil {
			t.Fatalf("Register %s: %v", tk.ID, err)
		}
	}

	// Filter by state
	pending := TaskStatePending
	result, err := ledger.List(ctx, TaskFilter{State: &pending})
	if err != nil {
		t.Fatalf("List by state: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("List(state=pending) returned %d, want 2", len(result))
	}

	// Filter by kind
	subAgent := TaskKindSubAgent
	result, err = ledger.List(ctx, TaskFilter{Kind: &subAgent})
	if err != nil {
		t.Fatalf("List by kind: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("List(kind=sub_agent) returned %d, want 2", len(result))
	}

	// Filter by parent
	parentID := "t1"
	result, err = ledger.List(ctx, TaskFilter{ParentID: &parentID})
	if err != nil {
		t.Fatalf("List by parent: %v", err)
	}
	if len(result) != 1 || result[0].ID != "t4" {
		t.Errorf("List(parent=t1) = %v, want [t4]", result)
	}

	// Combined filter
	result, err = ledger.List(ctx, TaskFilter{State: &pending, Kind: &subAgent})
	if err != nil {
		t.Fatalf("List combined: %v", err)
	}
	if len(result) != 1 || result[0].ID != "t2" {
		t.Errorf("List(state=pending, kind=sub_agent) = %v, want [t2]", result)
	}

	// No filter returns all
	result, err = ledger.List(ctx, TaskFilter{})
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(result) != 4 {
		t.Errorf("List(no filter) returned %d, want 4", len(result))
	}
}
