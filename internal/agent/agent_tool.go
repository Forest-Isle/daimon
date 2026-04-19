package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// agentToolInput is the JSON input format for AgentTool.
type agentToolInput struct {
	Task    string `json:"task"`
	Context string `json:"context,omitempty"`
}

// AgentTool wraps an AgentSpec as a tool.Tool, delegating all execution
// to SubAgentManager.
type AgentTool struct {
	spec    *AgentSpec
	manager *SubAgentManager
	breaker *CircuitBreaker
}

// NewAgentTool creates a new AgentTool for the given spec.
func NewAgentTool(spec *AgentSpec, manager *SubAgentManager) *AgentTool {
	return &AgentTool{
		spec:    spec,
		manager: manager,
		breaker: NewCircuitBreaker(),
	}
}

func (a *AgentTool) Name() string {
	return "agent_" + a.spec.Name
}

func (a *AgentTool) Description() string {
	return a.spec.Description
}

func (a *AgentTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "The task to delegate to this agent",
			},
			"context": map[string]any{
				"type":        "string",
				"description": "Optional context from previous tasks (predecessor outputs, etc.)",
			},
		},
		"required": []string{"task"},
	}
}

func (a *AgentTool) RequiresApproval() bool {
	return a.spec.RequiresApproval
}

// Execute delegates to SubAgentManager.Spawn for all execution modes.
func (a *AgentTool) Execute(ctx context.Context, input []byte) (tool.Result, error) {
	if err := a.breaker.Allow(); err != nil {
		return tool.Result{Error: err.Error()}, nil
	}

	var in agentToolInput
	if err := json.Unmarshal(input, &in); err != nil {
		a.breaker.RecordFailure()
		return tool.Result{Error: "invalid input: " + err.Error()}, nil
	}

	if in.Task == "" {
		a.breaker.RecordFailure()
		return tool.Result{Error: "task field is required"}, nil
	}

	timeout := a.spec.Timeout.Duration()
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	slog.Info("agent_tool: executing sub-agent",
		"agent", a.spec.Name,
		"task_len", len(in.Task),
		"timeout", timeout,
		"mode", a.spec.ExecutionMode,
	)

	parentRT := RuntimeFromContext(ctx)
	var parentID string
	var parentDepth int
	var chainID string
	if parentRT != nil {
		parentID = parentRT.AgentID()
		parentDepth = parentRT.Depth()
		chainID = parentRT.ChainID()
	}

	result, err := a.manager.Spawn(ctx, SpawnRequest{
		Spec:        a.spec,
		Task:        in.Task,
		TaskContext:  in.Context,
		ParentID:    parentID,
		ParentDepth: parentDepth,
		ChainID:     chainID,
	})

	if err != nil {
		a.breaker.RecordFailure()
		return tool.Result{Error: "sub-agent error: " + err.Error()}, nil
	}

	if result.Status == StatusError {
		a.breaker.RecordFailure()
		return tool.Result{Error: result.Error}, nil
	}

	a.breaker.RecordSuccess()
	slog.Info("agent_tool: sub-agent completed",
		"agent", a.spec.Name,
		"status", result.Status,
		"duration", result.Duration,
	)

	return tool.Result{Output: formatResultForParent(result)}, nil
}
