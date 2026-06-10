package evals

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// ── Eval 1: Plan create → update → complete lifecycle ───────────────
// Validates the plan tool correctly stores and retrieves plan state
// through session-scoped PlanStore.

func TestEval_PlanLifecycle(t *testing.T) {
	store := &stubPlanStore{plans: make(map[string]string)}
	pt := tool.NewPlanTool(store)
	ctx := tool.WithSessionID(context.Background(), "sess_eval_1")

	// Create a 3-step plan
	create := `{
		"operation": "create",
		"goal": "Fix nil pointer in handler.go",
		"steps": [
			{"id": "1", "description": "Find nil dereference", "criteria": "grep confirms", "status": "done"},
			{"id": "2", "description": "Add nil guard", "criteria": "go vet passes", "status": "in_progress"},
			{"id": "3", "description": "Add test", "criteria": "test_run passes", "status": "pending"}
		]
	}`
	result, err := pt.Execute(ctx, []byte(create))
	if err != nil || result.Error != "" {
		t.Fatalf("create failed: err=%v result.Error=%s", err, result.Error)
	}
	if !strings.Contains(result.Output, "2 step(s) remaining") {
		t.Errorf("expected 2 remaining, got: %s", result.Output)
	}

	// Verify stored plan
	raw, _ := store.GetPlan("sess_eval_1")
	var plan tool.Plan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatalf("stored plan invalid JSON: %v", err)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(plan.Steps))
	}

	// Progress: mark step 2 done, step 3 in_progress
	update := `{
		"operation": "update",
		"steps": [
			{"id": "1", "status": "done"},
			{"id": "2", "status": "done"},
			{"id": "3", "status": "in_progress"}
		]
	}`
	result2, _ := pt.Execute(ctx, []byte(update))
	if !strings.Contains(result2.Output, "1 step(s) remaining") {
		t.Errorf("expected 1 remaining, got: %s", result2.Output)
	}

	// Complete all steps
	complete := `{
		"operation": "update",
		"steps": [
			{"id": "1", "status": "done"},
			{"id": "2", "status": "done"},
			{"id": "3", "status": "done"}
		]
	}`
	result3, _ := pt.Execute(ctx, []byte(complete))
	if !strings.Contains(result3.Output, "All steps complete") {
		t.Errorf("expected 'All steps complete', got: %s", result3.Output)
	}
}

// ── Eval 2: VerifyInterceptor generates plan verification hint ──────
// When a write tool executes and the active plan has an in_progress step
// with test/lint criteria, the verify interceptor appends a hint.

func TestEval_VerifyInterceptorPlanHintGenerated(t *testing.T) {
	store := &stubPlanStore{plans: make(map[string]string)}
	plan := tool.Plan{
		Goal: "Refactor auth module",
		Steps: []tool.PlanStep{
			{ID: "1", Description: "Extract interface", Criteria: "go vet passes", Status: "done"},
			{ID: "2", Description: "Update callers", Criteria: "test_run passes", Status: "in_progress"},
			{ID: "3", Description: "Clean up", Criteria: "", Status: "pending"},
		},
	}
	raw, _ := json.Marshal(plan)
	store.SavePlan("sess_eval_2", string(raw))

	vi := tool.NewVerifyInterceptor(".", store)

	call := &tool.ToolCall{
		ToolName:  "file_write",
		Input:     `{"path": "auth.go", "content": "..."}`,
		SessionID: "sess_eval_2",
	}

	result, err := vi.Intercept(context.Background(), call, func(ctx context.Context, call *tool.ToolCall) (*tool.ToolResult, error) {
		return &tool.ToolResult{Output: "file written"}, nil
	})
	if err != nil {
		t.Fatalf("intercept failed: %v", err)
	}

	hint := result.Metadata["plan_verify_hint"]
	if hint == "" {
		t.Fatal("expected plan_verify_hint for in_progress step with test criteria")
	}
	if !strings.Contains(hint, "Update callers") {
		t.Errorf("hint should mention step description, got: %s", hint)
	}
	if !strings.Contains(strings.ToLower(hint), "test") {
		t.Errorf("hint should mention test verification, got: %s", hint)
	}
}

// ── Eval 3: No hint when all plan steps are done ────────────────────

func TestEval_NoVerifyHintWhenPlanComplete(t *testing.T) {
	store := &stubPlanStore{plans: make(map[string]string)}
	plan := tool.Plan{
		Goal: "Done task",
		Steps: []tool.PlanStep{
			{ID: "1", Description: "Did it", Criteria: "test_run passes", Status: "done"},
		},
	}
	raw, _ := json.Marshal(plan)
	store.SavePlan("sess_eval_3", string(raw))

	vi := tool.NewVerifyInterceptor(".", store)
	call := &tool.ToolCall{
		ToolName:  "file_write",
		Input:     `{"path": "x.go", "content": "x"}`,
		SessionID: "sess_eval_3",
	}

	result, err := vi.Intercept(context.Background(), call, func(ctx context.Context, call *tool.ToolCall) (*tool.ToolResult, error) {
		return &tool.ToolResult{Output: "ok"}, nil
	})
	if err != nil {
		t.Fatalf("intercept failed: %v", err)
	}
	if hint := result.Metadata["plan_verify_hint"]; hint != "" {
		t.Errorf("expected no hint when all steps done, got: %s", hint)
	}
}

// ── Eval 4: Plan status when no plan exists ─────────────────────────

func TestEval_PlanStatusWhenEmpty(t *testing.T) {
	store := &stubPlanStore{plans: make(map[string]string)}
	pt := tool.NewPlanTool(store)
	ctx := tool.WithSessionID(context.Background(), "sess_eval_empty")

	result, err := pt.Execute(ctx, []byte(`{"operation": "status"}`))
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !strings.Contains(result.Output, "No active plan") {
		t.Errorf("expected 'No active plan', got: %s", result.Output)
	}
}

// ── Eval 5: Plan tool rejects invalid operations ────────────────────

func TestEval_PlanRejectsInvalidInput(t *testing.T) {
	store := &stubPlanStore{plans: make(map[string]string)}
	pt := tool.NewPlanTool(store)
	ctx := tool.WithSessionID(context.Background(), "sess_eval_err")

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"create without goal", `{"operation": "create", "steps": [{"id":"1"}]}`, "goal"},
		{"create without steps", `{"operation": "create", "goal": "X"}`, "step"},
		{"update without existing plan", `{"operation": "update", "steps": [{"id":"1","status":"done"}]}`, "no existing plan"},
		{"unknown operation", `{"operation": "delete"}`, "unknown operation"},
		{"invalid JSON", `not json`, "invalid input"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := pt.Execute(ctx, []byte(tt.input))
			if !strings.Contains(strings.ToLower(result.Error+result.Output), tt.want) {
				t.Errorf("expected error containing %q, got: %s", tt.want, result.Error+result.Output)
			}
		})
	}
}

// ── Stubs ───────────────────────────────────────────────────────────

type stubPlanStore struct {
	plans map[string]string
}

func (s *stubPlanStore) GetPlan(sessionID string) (string, error) {
	return s.plans[sessionID], nil
}

func (s *stubPlanStore) SavePlan(sessionID string, planJSON string) error {
	s.plans[sessionID] = planJSON
	return nil
}
