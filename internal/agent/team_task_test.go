package agent

import (
	"testing"
)

func TestTeamTaskList_Create(t *testing.T) {
	tl := NewTeamTaskList()
	task := tl.Create("Implement feature", "Build the new auth module")

	if task.ID != "1" {
		t.Errorf("ID = %q, want %q", task.ID, "1")
	}
	if task.Subject != "Implement feature" {
		t.Errorf("Subject = %q, want %q", task.Subject, "Implement feature")
	}
	if task.Status != TaskPending {
		t.Errorf("Status = %q, want %q", task.Status, TaskPending)
	}

	task2 := tl.Create("Write tests", "Add unit tests")
	if task2.ID != "2" {
		t.Errorf("second ID = %q, want %q", task2.ID, "2")
	}
}

func TestTeamTaskList_Get(t *testing.T) {
	tl := NewTeamTaskList()
	tl.Create("Task 1", "desc 1")

	got := tl.Get("1")
	if got == nil {
		t.Fatal("Get(1) = nil")
	}
	if got.Subject != "Task 1" {
		t.Errorf("Subject = %q, want %q", got.Subject, "Task 1")
	}

	// Returns a copy — mutating it should not affect the original
	got.Subject = "mutated"
	original := tl.Get("1")
	if original.Subject != "Task 1" {
		t.Error("Get should return a copy, not a reference")
	}
}

func TestTeamTaskList_GetNotFound(t *testing.T) {
	tl := NewTeamTaskList()
	if tl.Get("999") != nil {
		t.Error("expected nil for nonexistent task")
	}
}

func TestTeamTaskList_UpdateStatus(t *testing.T) {
	tl := NewTeamTaskList()
	tl.Create("Task 1", "desc")

	if !tl.UpdateStatus("1", TaskInProgress) {
		t.Error("UpdateStatus should return true for existing task")
	}

	got := tl.Get("1")
	if got.Status != TaskInProgress {
		t.Errorf("Status = %q, want %q", got.Status, TaskInProgress)
	}

	if tl.UpdateStatus("999", TaskCompleted) {
		t.Error("UpdateStatus should return false for nonexistent task")
	}
}

func TestTeamTaskList_Assign(t *testing.T) {
	tl := NewTeamTaskList()
	tl.Create("Task 1", "desc")

	if !tl.Assign("1", "dev-1") {
		t.Error("Assign should return true for existing task")
	}

	got := tl.Get("1")
	if got.Owner != "dev-1" {
		t.Errorf("Owner = %q, want %q", got.Owner, "dev-1")
	}

	if tl.Assign("999", "dev-1") {
		t.Error("Assign should return false for nonexistent task")
	}
}

func TestTeamTaskList_Available(t *testing.T) {
	tl := NewTeamTaskList()
	tl.Create("Task 1", "desc 1")
	tl.Create("Task 2", "desc 2")
	tl.Create("Task 3", "desc 3")

	// Assign task 2
	tl.Assign("2", "dev-1")

	avail := tl.Available()
	if len(avail) != 2 {
		t.Fatalf("Available = %d, want 2", len(avail))
	}

	ids := map[string]bool{}
	for _, a := range avail {
		ids[a.ID] = true
	}
	if !ids["1"] || !ids["3"] {
		t.Errorf("expected tasks 1 and 3, got %v", ids)
	}
}

func TestTeamTaskList_AvailableExcludesBlocked(t *testing.T) {
	tl := NewTeamTaskList()
	t1 := tl.Create("Task 1", "desc 1")
	_ = t1

	// Manually create a blocked task
	tl.mu.Lock()
	tl.tasks = append(tl.tasks, &TeamTask{
		ID:        "blocked",
		Subject:   "Blocked task",
		Status:    TaskPending,
		BlockedBy: []string{"1"},
	})
	tl.mu.Unlock()

	avail := tl.Available()
	if len(avail) != 1 {
		t.Fatalf("Available = %d, want 1", len(avail))
	}
	if avail[0].ID != "1" {
		t.Errorf("expected task 1, got %s", avail[0].ID)
	}
}

func TestTeamTaskList_All(t *testing.T) {
	tl := NewTeamTaskList()
	tl.Create("Task 1", "desc 1")
	tl.Create("Task 2", "desc 2")
	tl.Assign("1", "dev-1")
	tl.UpdateStatus("1", TaskInProgress)

	all := tl.All()
	if len(all) != 2 {
		t.Fatalf("All = %d, want 2", len(all))
	}

	// Verify it returns copies
	all[0].Subject = "mutated"
	original := tl.Get("1")
	if original.Subject != "Task 1" {
		t.Error("All should return copies")
	}
}
