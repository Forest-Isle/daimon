package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/dag"
	"github.com/Forest-Isle/IronClaw/internal/hook"
)

// SingleToolExecutor executes a single tool by name with raw JSON input.
// This interface breaks the circular dependency: tool package cannot import agent.
type SingleToolExecutor interface {
	Execute(ctx context.Context, toolName, input string) (output string, err error)
}

// PlanTaskTool decomposes complex multi-step tasks into subtasks with dependency
// tracking and parallel execution via the DAG executor.
// The tool itself does not execute subtasks directly; it delegates to a
// SingleToolExecutor (implemented by the agent package via ToolExecutor bridge).
type PlanTaskTool struct {
	maxParallel    int
	singleExecutor SingleToolExecutor
	hookMgr        *hook.Manager
	permEngine     *PermissionEngine
	interceptor    *InterceptorChain
}

// NewPlanTaskTool creates a PlanTaskTool with an optional executor bridge.
// maxParallel controls the maximum number of concurrently executing subtasks
// (defaults to 5 when <= 0).
func NewPlanTaskTool(maxParallel int, executor SingleToolExecutor,
	hookMgr *hook.Manager, permEngine *PermissionEngine, interceptor *InterceptorChain) *PlanTaskTool {
	if maxParallel <= 0 {
		maxParallel = 5
	}
	return &PlanTaskTool{
		maxParallel:    maxParallel,
		singleExecutor: executor,
		hookMgr:        hookMgr,
		permEngine:     permEngine,
		interceptor:    interceptor,
	}
}

func (p *PlanTaskTool) Name() string            { return "plan_task" }
func (p *PlanTaskTool) RequiresApproval() bool   { return false }
func (p *PlanTaskTool) IsReadOnly() bool         { return false }

func (p *PlanTaskTool) Description() string {
	return "Decompose a complex request into independently executable subtasks with dependency tracking. " +
		"Subtasks with no mutual dependencies run in parallel. " +
		"Use this when a request requires multiple distinct operations across different tools. " +
		"For single-tool requests, call the tool directly instead. " +
		"Each subtask needs: id, description, tool_name, tool_input, depends_on (optional array of task IDs), confidence (0.0-1.0)."
}

// Capabilities returns the plan_task tool's capabilities.
func (p *PlanTaskTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		IsReadOnly:      false,
		IsDestructive:   true, // orchestrates other tools that may have side effects
		RequiresNetwork: false,
		ApprovalMode:    "auto",
	}
}

func (p *PlanTaskTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subtasks": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id": map[string]any{
							"type":        "string",
							"description": "Unique identifier for this subtask",
						},
						"description": map[string]any{
							"type":        "string",
							"description": "What this task does, in imperative form",
						},
						"tool_name": map[string]any{
							"type":        "string",
							"description": "The tool to invoke (bash, read, write, edit, etc.)",
						},
						"tool_input": map[string]any{
							"type":        "string",
							"description": "JSON input for the tool",
						},
						"depends_on": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "IDs of tasks that must complete first",
						},
						"confidence": map[string]any{
							"type":        "number",
							"description": "How confident you are this task will succeed (0.0-1.0)",
						},
					},
					"required": []string{"id", "description", "tool_name", "tool_input"},
				},
			},
		},
		"required": []string{"subtasks"},
	}
}

func (p *PlanTaskTool) Execute(ctx context.Context, input []byte) (Result, error) {
	var plan struct {
		SubTasks []struct {
			ID          string   `json:"id"`
			Description string   `json:"description"`
			ToolName    string   `json:"tool_name"`
			ToolInput   string   `json:"tool_input"`
			DependsOn   []string `json:"depends_on"`
			Confidence  float64  `json:"confidence"`
		} `json:"subtasks"`
	}
	if err := json.Unmarshal(input, &plan); err != nil {
		return Result{Error: fmt.Sprintf("plan_task: invalid JSON input: %v", err)}, nil
	}
	if len(plan.SubTasks) == 0 {
		return Result{Output: "No subtasks provided."}, nil
	}
	if p.singleExecutor == nil {
		return Result{Error: "plan_task: no tool executor configured"}, nil
	}

	dagTasks := make([]dag.Task, len(plan.SubTasks))
	for i, st := range plan.SubTasks {
		dagTasks[i] = dag.Task{
			ID:          st.ID,
			Description: st.Description,
			ToolName:    st.ToolName,
			ToolInput:   st.ToolInput,
			DependsOn:   st.DependsOn,
		}
	}

	execFn := func(ctx context.Context, t dag.Task) dag.Result {
		start := time.Now()
		if t.ToolName == "" {
			return dag.Result{
				TaskID: t.ID, Output: t.Description,
				Status: dag.StatusDone, DurationMs: time.Since(start).Milliseconds(),
			}
		}
		output, err := p.singleExecutor.Execute(ctx, t.ToolName, t.ToolInput)
		dur := time.Since(start).Milliseconds()
		if err != nil {
			return dag.Result{
				TaskID: t.ID, Error: err.Error(),
				DurationMs: dur, Status: dag.StatusFailed,
			}
		}
		return dag.Result{
			TaskID: t.ID, Output: output,
			DurationMs: dur, Status: dag.StatusDone,
		}
	}

	results := dag.Execute(ctx, dagTasks, execFn, p.maxParallel)
	formatted := dag.FormatResults(results)
	return Result{Output: formatted}, nil
}
