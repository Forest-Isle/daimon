package taskledger

import (
	"testing"
)

func TestParseTaskPlan_Valid(t *testing.T) {
	raw := `[
		{"id": "t1", "title": "Setup DB", "description": "Create tables", "depends_on": []},
		{"id": "t2", "title": "Build API", "description": "REST endpoints", "depends_on": ["t1"]},
		{"id": "t3", "title": "Write tests", "description": "Unit tests for API", "depends_on": ["t1"]}
	]`

	tasks, err := ParseTaskPlan(raw, "root-1")
	if err != nil {
		t.Fatalf("ParseTaskPlan: %v", err)
	}

	if len(tasks) != 3 {
		t.Fatalf("got %d tasks, want 3", len(tasks))
	}

	if tasks[0].ID != "t1" || tasks[0].Title != "Setup DB" || tasks[0].Description != "Create tables" {
		t.Errorf("task 0 = %+v", tasks[0])
	}
	if tasks[1].ID != "t2" || tasks[1].Title != "Build API" {
		t.Errorf("task 1 = %+v", tasks[1])
	}

	for _, task := range tasks {
		if task.ParentID != "root-1" {
			t.Errorf("task %s ParentID = %q, want %q", task.ID, task.ParentID, "root-1")
		}
		if task.Kind != TaskKindTeamTask {
			t.Errorf("task %s Kind = %q, want %q", task.ID, task.Kind, TaskKindTeamTask)
		}
		if task.State != TaskStatePending {
			t.Errorf("task %s State = %q, want %q", task.ID, task.State, TaskStatePending)
		}
	}

	if len(tasks[1].DependsOn) != 1 || tasks[1].DependsOn[0] != "t1" {
		t.Errorf("task t2 DependsOn = %v, want [t1]", tasks[1].DependsOn)
	}
	if len(tasks[2].DependsOn) != 1 || tasks[2].DependsOn[0] != "t1" {
		t.Errorf("task t3 DependsOn = %v, want [t1]", tasks[2].DependsOn)
	}
}

func TestParseTaskPlan_InvalidJSON(t *testing.T) {
	_, err := ParseTaskPlan("not valid json at all", "root-1")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestParseTaskPlan_InvalidDependency(t *testing.T) {
	raw := `[
		{"id": "t1", "title": "Task A", "description": "desc", "depends_on": ["nonexistent"]}
	]`

	_, err := ParseTaskPlan(raw, "root-1")
	if err == nil {
		t.Fatal("expected error for invalid dependency, got nil")
	}
}

func TestParseTaskPlan_DuplicateIDs(t *testing.T) {
	raw := `[
		{"id": "t1", "title": "First", "description": "desc", "depends_on": []},
		{"id": "t1", "title": "Duplicate", "description": "desc", "depends_on": []}
	]`

	_, err := ParseTaskPlan(raw, "root-1")
	if err == nil {
		t.Fatal("expected error for duplicate IDs, got nil")
	}
}
