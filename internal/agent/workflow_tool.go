package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Forest-Isle/daimon/internal/tool"
	"github.com/Forest-Isle/daimon/internal/workflow"
)

type WorkflowTool struct {
	agentMgr *AgentManager
	subMgr   *SubAgentManager
	cache    workflow.ReplayCache
	bus      EventBus
}

type workflowToolInput struct {
	Spec string `json:"spec"`
}

func NewWorkflowTool(agentMgr *AgentManager, subMgr *SubAgentManager, cache workflow.ReplayCache, buses ...EventBus) *WorkflowTool {
	t := &WorkflowTool{agentMgr: agentMgr, subMgr: subMgr, cache: cache}
	if len(buses) > 0 {
		t.bus = buses[0]
	}
	return t
}

func (t *WorkflowTool) Name() string { return "workflow" }

func (t *WorkflowTool) Description() string {
	return strings.TrimSpace(`
Execute a deterministic multi-agent workflow.

Use this for high-value tasks that need explicit pipeline orchestration,
structured sub-agent outputs, replay caching, and per-run budgets. Workflow
execution is pipeline-first: stages run in order; only stages marked
parallel=true run their steps concurrently as a barrier.
`)
}

func (t *WorkflowTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"spec": map[string]any{
				"type":        "string",
				"description": "YAML or JSON workflow spec. Steps currently support type=agent with agent and task fields.",
			},
		},
		"required": []string{"spec"},
	}
}

func (t *WorkflowTool) RequiresApproval() bool { return false }

func (t *WorkflowTool) IsReadOnly() bool { return false }

func (t *WorkflowTool) Capabilities() tool.ToolCapabilities {
	return tool.ToolCapabilities{
		IsReadOnly:      false,
		IsDestructive:   false,
		RequiresNetwork: true,
		ApprovalMode:    "auto",
		ParallelSafety:  tool.ParallelNever,
	}
}

func (t *WorkflowTool) Execute(ctx context.Context, input []byte) (tool.Result, error) {
	var in workflowToolInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.Result{Error: "workflow: invalid input: " + err.Error()}, nil
	}
	spec, err := workflow.ParseSpec([]byte(in.Spec))
	if err != nil {
		return tool.Result{Error: err.Error()}, nil
	}
	executor := workflow.Executor{
		Runner:      &agentWorkflowRunner{agentMgr: t.agentMgr, subMgr: t.subMgr},
		Cache:       t.cache,
		Observer:    workflowObserver{bus: t.bus},
		MaxParallel: 3,
	}
	run, err := executor.Execute(ctx, spec)
	if err != nil {
		return tool.Result{Error: err.Error()}, nil
	}
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return tool.Result{Error: "workflow: marshal run: " + err.Error()}, nil
	}
	if run.Status == workflow.RunFailed {
		return tool.Result{Output: string(data), Metadata: map[string]any{"workflow_status": string(run.Status)}}, nil
	}
	return tool.Result{Output: string(data), Metadata: map[string]any{"workflow_status": string(run.Status)}}, nil
}

type workflowObserver struct {
	bus EventBus
}

func (o workflowObserver) ObserveWorkflowStep(_ context.Context, event workflow.StepEvent) {
	if o.bus == nil {
		return
	}
	o.bus.Publish(WorkflowStepEvent{
		WorkflowName: event.WorkflowName,
		WorkflowHash: event.WorkflowHash,
		StageID:      event.StageID,
		StepID:       event.StepID,
		StepType:     string(event.StepType),
		Phase:        event.Phase,
		Status:       string(event.Status),
		Cached:       event.Cached,
		DurationMs:   event.DurationMillis,
		Error:        event.Error,
	})
}

type agentWorkflowRunner struct {
	agentMgr *AgentManager
	subMgr   *SubAgentManager
}

func (r *agentWorkflowRunner) RunStep(ctx context.Context, step workflow.Step, input workflow.StepInput) (workflow.StepOutput, error) {
	if step.Type != workflow.StepTypeAgent {
		return workflow.StepOutput{Status: workflow.StatusError}, fmt.Errorf("workflow step type %q is not supported by agent runner", step.Type)
	}
	if r == nil || r.agentMgr == nil || r.subMgr == nil {
		return workflow.StepOutput{Status: workflow.StatusError}, fmt.Errorf("workflow agent runner is not available")
	}
	spec, ok := r.agentMgr.Get(step.Agent)
	if !ok {
		return workflow.StepOutput{Status: workflow.StatusError}, fmt.Errorf("unknown workflow agent %q", step.Agent)
	}
	spec.ExecutionMode = ExecModeSpawn
	start := time.Now()
	// Link the spawned worker to the parent session/depth so its tool activity
	// surfaces nested in the parent transcript (same as the agent_<name> and
	// agent_dispatch paths). The parent session id rides on ctx via WithSessionID.
	var parentID string
	var parentDepth int
	if pa := AgentFromContext(ctx); pa != nil {
		parentID = pa.AgentID()
	}
	if sc := SubagentContextFromCtx(ctx); sc != nil {
		parentDepth = sc.Depth
	}
	result, err := r.subMgr.Spawn(ctx, SpawnRequest{
		Spec:            spec,
		Task:            step.Task,
		TaskContext:     workflowContext(input.PriorResults),
		ParentID:        parentID,
		ParentDepth:     parentDepth,
		ParentSessionID: tool.SessionIDFromContext(ctx),
	})
	if err != nil {
		return workflow.StepOutput{Status: workflow.StatusError}, err
	}
	status := workflow.StatusSuccess
	if result.Status == StatusError || result.Status == StatusTimeout {
		status = workflow.StatusError
	}
	output := result.Output
	if output == "" {
		output = result.Summary
	}
	return workflow.StepOutput{
		Status:     status,
		Summary:    result.Summary,
		Output:     output,
		Artifacts:  result.Artifacts,
		TokensUsed: result.TokensUsed,
		Metadata: map[string]any{
			"agent":       step.Agent,
			"duration_ms": time.Since(start).Milliseconds(),
			"status":      string(result.Status),
		},
	}, nil
}

func workflowContext(results []workflow.StepResult) string {
	if len(results) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Workflow context from completed prior steps:\n")
	for _, result := range results {
		fmt.Fprintf(&b, "- %s [%s]", result.StepID, result.Status)
		if result.Summary != "" {
			fmt.Fprintf(&b, ": %s", result.Summary)
		} else if result.Output != "" {
			fmt.Fprintf(&b, ": %s", truncateWorkflowContext(result.Output, 400))
		}
		if len(result.Artifacts) > 0 {
			fmt.Fprintf(&b, " artifacts=%s", strings.Join(result.Artifacts, ", "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func truncateWorkflowContext(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if max > 3 && len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}
