package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Forest-Isle/daimon/internal/tool"
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
		breaker: NewCircuitBreaker(3, 15*time.Second),
	}
}

func (a *AgentTool) Name() string {
	return "agent_" + a.spec.Name
}

func (a *AgentTool) Description() string {
	desc := a.spec.Description
	if desc == "" {
		desc = fmt.Sprintf("Delegate a task to the %s agent.", a.spec.Name)
	}
	// Append capability summary so the parent LLM knows what this agent can do.
	if len(a.spec.Tools) > 0 {
		desc += fmt.Sprintf(" Available tools: %s.", strings.Join(a.spec.Tools, ", "))
	}
	if a.spec.MaxIterations > 0 {
		desc += fmt.Sprintf(" Max iterations: %d.", a.spec.MaxIterations)
	}
	if a.spec.Model != "" {
		desc += fmt.Sprintf(" Model: %s.", a.spec.Model)
	}
	return desc
}

func (a *AgentTool) InputSchema() map[string]any {
	desc := fmt.Sprintf("The task for the %s agent to execute.", a.spec.Name)
	if a.spec.Description != "" {
		desc = a.spec.Description
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": desc,
			},
			"context": map[string]any{
				"type":        "string",
				"description": "Optional context from previous tasks or parent agent output.",
			},
		},
		"required": []string{"task"},
	}
}

func (a *AgentTool) RequiresApproval() bool {
	return a.spec.RequiresApproval
}

// IsReadOnly returns false — sub-agents typically perform actions, not just reads.
func (a *AgentTool) IsReadOnly() bool { return false }

// Capabilities returns the tool capabilities for concurrent execution and security.
func (a *AgentTool) Capabilities() tool.ToolCapabilities {
	approval := "auto"
	if a.spec.RequiresApproval {
		approval = "always"
	}
	return tool.ToolCapabilities{
		IsReadOnly:      false,
		IsDestructive:   false, // sub-agents may modify state, but it's scoped
		RequiresNetwork: true,
		ApprovalMode:    approval,
		ParallelSafety:  tool.ParallelNever, // sub-agents consume significant resources
	}
}

// Execute delegates to SubAgentManager.Spawn for all execution modes.
func (a *AgentTool) Execute(ctx context.Context, input []byte) (tool.Result, error) {
	if !a.breaker.Allow() {
		return tool.Result{Error: "circuit breaker open: agent tool temporarily unavailable"}, nil
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

	parentAgent := AgentFromContext(ctx)
	var parentID string
	var parentDepth int
	var chainID string
	if parentAgent != nil {
		parentID = parentAgent.AgentID()
	}
	if sc := SubagentContextFromCtx(ctx); sc != nil {
		if parentDepth == 0 {
			parentDepth = sc.Depth
		}
		if chainID == "" {
			chainID = sc.ChainID
		}
	}

	result, err := a.manager.Spawn(ctx, SpawnRequest{
		Spec:        a.spec,
		Task:        in.Task,
		TaskContext: in.Context,
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
