package taskledger

import (
	"encoding/json"
	"fmt"
	"time"
)

// TeamPlanPrompt is the LLM prompt template for generating parallelizable task plans.
const TeamPlanPrompt = `You are a task planner. Break the following goal into independent, parallelizable tasks.

Output a JSON array where each task has:
- "id": unique short identifier (e.g., "t1", "t2")
- "title": concise task title
- "description": detailed instructions for an agent to execute this task
- "depends_on": array of task IDs that must complete before this task can start (empty array if no dependencies)

Rules:
- Maximize parallelism: only add dependencies when truly necessary
- Each task should be independently executable by an agent
- Keep tasks focused and atomic

Goal: %s`

type rawTaskPlan struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	DependsOn   []string `json:"depends_on"`
}

// ParseTaskPlan parses an LLM-generated JSON task list into Task structs.
// Expected JSON format:
//
//	[
//	  {"id": "t1", "title": "...", "description": "...", "depends_on": []},
//	  {"id": "t2", "title": "...", "description": "...", "depends_on": ["t1"]}
//	]
func ParseTaskPlan(raw string, parentID string) ([]Task, error) {
	var plans []rawTaskPlan
	if err := json.Unmarshal([]byte(raw), &plans); err != nil {
		return nil, fmt.Errorf("invalid task plan JSON: %w", err)
	}

	if len(plans) == 0 {
		return nil, fmt.Errorf("task plan is empty")
	}

	seen := make(map[string]bool, len(plans))
	for _, p := range plans {
		if p.ID == "" {
			return nil, fmt.Errorf("task has empty ID")
		}
		if seen[p.ID] {
			return nil, fmt.Errorf("duplicate task ID: %q", p.ID)
		}
		seen[p.ID] = true
	}

	for _, p := range plans {
		for _, dep := range p.DependsOn {
			if !seen[dep] {
				return nil, fmt.Errorf("task %q depends on unknown ID %q", p.ID, dep)
			}
		}
	}

	now := time.Now().UTC()
	tasks := make([]Task, len(plans))
	for i, p := range plans {
		tasks[i] = Task{
			ID:          p.ID,
			ParentID:    parentID,
			Kind:        TaskKindTeamTask,
			State:       TaskStatePending,
			Title:       p.Title,
			Description: p.Description,
			DependsOn:   p.DependsOn,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
	}
	return tasks, nil
}
