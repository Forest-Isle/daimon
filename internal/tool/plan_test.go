package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type testPlanStore struct {
	plans map[string]string
}

func (s *testPlanStore) GetPlan(sessionID string) (string, error) {
	return s.plans[sessionID], nil
}

func (s *testPlanStore) SavePlan(sessionID string, planJSON string) error {
	s.plans[sessionID] = planJSON
	return nil
}

func TestPlanTool_CreateAndStatus(t *testing.T) {
	store := &testPlanStore{plans: make(map[string]string)}
	pt := NewPlanTool(store)

	ctx := WithSessionID(context.Background(), "sess_1")

	// Create a plan
	createInput := `{
		"operation": "create",
		"goal": "Fix bug in foo.go",
		"steps": [
			{"id": "1", "description": "Read the code", "criteria": "", "status": "done"},
			{"id": "2", "description": "Apply fix", "criteria": "test_run passes", "status": "in_progress"},
			{"id": "3", "description": "Commit", "criteria": "", "status": "pending"}
		]
	}`
	result, err := pt.Execute(ctx, []byte(createInput))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "Fix bug in foo.go") {
		t.Errorf("expected goal in output, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "2 step(s) remaining") {
		t.Errorf("expected '2 step(s) remaining', got: %s", result.Output)
	}

	// Check stored plan
	raw, err := store.GetPlan("sess_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var plan Plan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatalf("invalid stored plan: %v", err)
	}
	if plan.Goal != "Fix bug in foo.go" {
		t.Errorf("expected goal, got: %s", plan.Goal)
	}
	if len(plan.Steps) != 3 {
		t.Errorf("expected 3 steps, got: %d", len(plan.Steps))
	}

	// Status
	statusResult, err := pt.Execute(ctx, []byte(`{"operation": "status"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(statusResult.Output, "Fix bug in foo.go") {
		t.Errorf("status should show plan, got: %s", statusResult.Output)
	}
}

func TestPlanTool_UpdateMarksStepDone(t *testing.T) {
	store := &testPlanStore{plans: make(map[string]string)}
	pt := NewPlanTool(store)
	ctx := WithSessionID(context.Background(), "sess_1")

	// Create initial plan
	createInput := `{
		"operation": "create",
		"goal": "Add feature X",
		"steps": [
			{"id": "1", "description": "Write code", "criteria": "tests pass", "status": "done"},
			{"id": "2", "description": "Review", "criteria": "lint clean", "status": "pending"}
		]
	}`
	pt.Execute(ctx, []byte(createInput))

	// Update step 2 to done
	updateInput := `{
		"operation": "update",
		"steps": [
			{"id": "1", "status": "done"},
			{"id": "2", "status": "done"}
		]
	}`
	result, err := pt.Execute(ctx, []byte(updateInput))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "All steps complete") {
		t.Errorf("expected 'All steps complete', got: %s", result.Output)
	}

	// Verify stored state
	raw, _ := store.GetPlan("sess_1")
	var plan Plan
	json.Unmarshal([]byte(raw), &plan)
	for _, s := range plan.Steps {
		if s.Status != "done" {
			t.Errorf("step %s expected done, got %s", s.ID, s.Status)
		}
	}
}

func TestPlanTool_CreateRequiresGoalAndSteps(t *testing.T) {
	store := &testPlanStore{plans: make(map[string]string)}
	pt := NewPlanTool(store)
	ctx := WithSessionID(context.Background(), "sess_1")

	// Missing goal
	result, _ := pt.Execute(ctx, []byte(`{"operation": "create", "steps": []}`))
	if !strings.Contains(result.Error, "goal") {
		t.Errorf("expected goal error, got: %s", result.Error)
	}

	// Missing steps
	result, _ = pt.Execute(ctx, []byte(`{"operation": "create", "goal": "X"}`))
	if !strings.Contains(result.Error, "step") {
		t.Errorf("expected steps error, got: %s", result.Error)
	}
}

func TestPlanTool_NoSessionID(t *testing.T) {
	store := &testPlanStore{plans: make(map[string]string)}
	pt := NewPlanTool(store)
	ctx := context.Background() // no session ID

	result, _ := pt.Execute(ctx, []byte(`{"operation": "status"}`))
	if !strings.Contains(result.Error, "no session") {
		t.Errorf("expected session error, got: %s", result.Error)
	}
}

func TestPlanTool_InvalidJSON(t *testing.T) {
	store := &testPlanStore{plans: make(map[string]string)}
	pt := NewPlanTool(store)
	ctx := WithSessionID(context.Background(), "sess_1")

	result, _ := pt.Execute(ctx, []byte(`not json`))
	if !strings.Contains(result.Error, "invalid input") {
		t.Errorf("expected invalid input error, got: %s", result.Error)
	}
}
