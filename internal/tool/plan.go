package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// PlanStore reads and writes serialised plan state scoped to a session.
type PlanStore interface {
	GetPlan(sessionID string) (string, error)
	SavePlan(sessionID string, planJSON string) error
}

// PlanStep represents a single step in an execution plan.
type PlanStep struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Criteria    string `json:"criteria"` // how to verify this step succeeded
	Status      string `json:"status"`   // pending | in_progress | done | failed
}

// Plan is the top-level execution plan managed by the model.
type Plan struct {
	Goal      string     `json:"goal"`
	Steps     []PlanStep `json:"steps"`
	UpdatedAt string     `json:"updated_at"`
}

// PlanTool gives the model an explicit plan/todo mechanism so multi-step
// tasks are self-documenting and verifiable. The plan is persisted in
// session metadata and re-injected into the system prompt each iteration.
type PlanTool struct {
	store PlanStore
}

// NewPlanTool creates a PlanTool backed by the given PlanStore.
func NewPlanTool(store PlanStore) *PlanTool {
	return &PlanTool{store: store}
}

func (t *PlanTool) Name() string           { return "plan" }
func (t *PlanTool) RequiresApproval() bool { return false }
func (t *PlanTool) IsReadOnly() bool       { return false }

func (t *PlanTool) Description() string {
	return strings.TrimSpace(`
Manage a step-by-step task plan with success criteria for each step.

Use this tool to create or update your plan before and during task execution.
Each step should have clear, verifiable success criteria (e.g., "test_run passes",
"go vet is clean", "file exists at path X").

Operations:
- create:  set a new plan (overwrites any existing plan)
- update:  update an existing plan's goal or steps
- status:  read the current plan

When all steps are marked "done", the task is complete.
`)
}

func (t *PlanTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operation": map[string]any{
				"type":        "string",
				"description": "Operation: create, update, or status",
				"enum":        []string{"create", "update", "status"},
			},
			"goal": map[string]any{
				"type":        "string",
				"description": "One-line goal for the overall task (required for create/update)",
			},
			"steps": map[string]any{
				"type":        "array",
				"description": "Ordered list of plan steps (required for create/update)",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":          map[string]any{"type": "string", "description": "Unique step id (e.g. '1', '2a')"},
						"description": map[string]any{"type": "string", "description": "What this step does"},
						"criteria":    map[string]any{"type": "string", "description": "How to verify this step succeeded"},
						"status":      map[string]any{"type": "string", "description": "pending, in_progress, done, or failed", "enum": []string{"pending", "in_progress", "done", "failed"}},
					},
				},
			},
		},
		"required": []string{"operation"},
	}
}

func (t *PlanTool) Execute(ctx context.Context, input []byte) (Result, error) {
	sessionID := SessionIDFromContext(ctx)
	if sessionID == "" {
		return Result{Error: "plan tool: no session ID in context"}, nil
	}

	var req struct {
		Operation string     `json:"operation"`
		Goal      string     `json:"goal"`
		Steps     []PlanStep `json:"steps"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return Result{Error: fmt.Sprintf("plan: invalid input: %v", err)}, nil
	}

	switch req.Operation {
	case "status":
		return t.handleStatus(sessionID)
	case "create":
		return t.handleCreate(sessionID, req.Goal, req.Steps)
	case "update":
		return t.handleUpdate(sessionID, req.Goal, req.Steps)
	default:
		return Result{Error: fmt.Sprintf("plan: unknown operation %q (valid: create, update, status)", req.Operation)}, nil
	}
}

func (t *PlanTool) handleStatus(sessionID string) (Result, error) {
	raw, err := t.store.GetPlan(sessionID)
	if err != nil {
		return Result{Error: fmt.Sprintf("plan: read failed: %v", err)}, nil
	}
	if raw == "" {
		return Result{Output: "No active plan. Use operation=create to start one."}, nil
	}
	return Result{Output: raw}, nil
}

func (t *PlanTool) handleCreate(sessionID, goal string, steps []PlanStep) (Result, error) {
	if goal == "" {
		return Result{Error: "plan create: goal is required"}, nil
	}
	if len(steps) == 0 {
		return Result{Error: "plan create: at least one step is required"}, nil
	}
	for i := range steps {
		if steps[i].Status == "" {
			steps[i].Status = "pending"
		}
	}
	plan := Plan{Goal: goal, Steps: steps, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	return t.saveAndReturn(sessionID, plan)
}

func (t *PlanTool) handleUpdate(sessionID, goal string, steps []PlanStep) (Result, error) {
	raw, err := t.store.GetPlan(sessionID)
	if err != nil {
		return Result{Error: fmt.Sprintf("plan: read failed: %v", err)}, nil
	}
	if raw == "" {
		return Result{Error: "plan update: no existing plan to update (use create first)"}, nil
	}

	var plan Plan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return Result{Error: fmt.Sprintf("plan: corrupt plan: %v", err)}, nil
	}

	// Merge updates: only overwrite fields that were provided
	if goal != "" {
		plan.Goal = goal
	}
	if len(steps) > 0 {
		// Merge step statuses by ID — keep existing steps not mentioned
		existingByID := make(map[string]PlanStep, len(plan.Steps))
		for _, s := range plan.Steps {
			existingByID[s.ID] = s
		}
		merged := make([]PlanStep, 0, len(steps))
		for _, s := range steps {
			if existing, ok := existingByID[s.ID]; ok {
				// Preserve non-empty fields from the update, fall back to existing
				if s.Description == "" {
					s.Description = existing.Description
				}
				if s.Criteria == "" {
					s.Criteria = existing.Criteria
				}
				if s.Status == "" {
					s.Status = existing.Status
				}
			}
			if s.Status == "" {
				s.Status = "pending"
			}
			merged = append(merged, s)
		}
		plan.Steps = merged
	}
	plan.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	return t.saveAndReturn(sessionID, plan)
}

func (t *PlanTool) saveAndReturn(sessionID string, plan Plan) (Result, error) {
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return Result{Error: fmt.Sprintf("plan: marshal failed: %v", err)}, nil
	}
	if err := t.store.SavePlan(sessionID, string(data)); err != nil {
		return Result{Error: fmt.Sprintf("plan: save failed: %v", err)}, nil
	}

	// Build a summary for the model
	var b strings.Builder
	fmt.Fprintf(&b, "Plan saved: **%s**\n\n", plan.Goal)
	pending := 0
	for _, s := range plan.Steps {
		icon := map[string]string{"pending": "○", "in_progress": "◉", "done": "✓", "failed": "✗"}[s.Status]
		fmt.Fprintf(&b, "%s [%s] %s", icon, s.Status, s.Description)
		if s.Criteria != "" {
			fmt.Fprintf(&b, "  — verify: %s", s.Criteria)
		}
		b.WriteString("\n")
		if s.Status != "done" && s.Status != "failed" {
			pending++
		}
	}
	if pending > 0 {
		fmt.Fprintf(&b, "\n%d step(s) remaining. Update status via plan update as you progress.", pending)
	} else {
		b.WriteString("\nAll steps complete! If the task is done, return without further tool calls.")
	}
	return Result{Output: b.String()}, nil
}
