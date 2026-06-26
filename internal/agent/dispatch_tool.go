package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Forest-Isle/daimon/internal/mind"
	"github.com/Forest-Isle/daimon/internal/tool"
)

// dispatchToolInput is the JSON input for agent_dispatch.
type dispatchToolInput struct {
	Description string   `json:"description"`
	Prompt      string   `json:"prompt,omitempty"`
	Task        string   `json:"task"`
	Tools       []string `json:"tools,omitempty"`
	Model       string   `json:"model,omitempty"`
	Agent       string   `json:"agent,omitempty"`
}

// defaultDispatchTools is the read-only tool set an ad-hoc worker gets when the
// caller does not specify `tools`. IronClaw fences tools by posture, so write/
// exec tools must be granted explicitly.
var defaultDispatchTools = []string{"file_read", "grep_code", "find_symbol"}

// DispatchTool spawns an ephemeral sub-agent from an inline definition — no
// pre-registered AgentSpec required. It is the general-purpose delegation tool.
type DispatchTool struct {
	manager *SubAgentManager
	agents  *AgentManager // optional; resolves an `agent` reference to a registered spec
	breaker *mind.CircuitBreaker
}

// NewDispatchTool creates the agent_dispatch tool. agents may be nil (then the
// `agent` reference field is ignored and inline definitions are always used).
func NewDispatchTool(manager *SubAgentManager, agents *AgentManager) *DispatchTool {
	return &DispatchTool{
		manager: manager,
		agents:  agents,
		breaker: mind.NewCircuitBreaker(3, 15*time.Second),
	}
}

func (t *DispatchTool) Name() string { return "agent_dispatch" }

func (t *DispatchTool) Description() string {
	return "Dispatch an ephemeral sub-agent to do a focused task. Provide `task` and a `prompt` describing the worker; optionally `tools` (default: read-only file_read/grep_code/find_symbol), `model`, or `agent` to reference a registered agent. The worker runs isolated and returns its findings."
}

func (t *DispatchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task":        map[string]any{"type": "string", "description": "The concrete task for the worker."},
			"prompt":      map[string]any{"type": "string", "description": "System prompt describing the worker's role."},
			"description": map[string]any{"type": "string", "description": "Short description of when/what this worker is for."},
			"tools":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Allowlist of tool names; empty = read-only default set."},
			"model":       map[string]any{"type": "string", "description": "Optional model override."},
			"agent":       map[string]any{"type": "string", "description": "Optional: name of a registered agent to use instead of an inline definition."},
		},
		"required": []string{"task"},
	}
}

func (t *DispatchTool) RequiresApproval() bool { return true }

// buildDispatchSpec constructs an ephemeral AgentSpec from inline input,
// applying the read-only default toolset and a description fallback (Validate
// requires a non-empty description).
func buildDispatchSpec(in dispatchToolInput) *AgentSpec {
	desc := in.Description
	if desc == "" {
		desc = "Ad-hoc dispatched worker."
	}
	tools := in.Tools
	if len(tools) == 0 {
		tools = defaultDispatchTools
	}
	return &AgentSpec{
		Name:          "dispatch",
		Description:   desc,
		SystemPrompt:  in.Prompt,
		Tools:         tools,
		Model:         in.Model,
		MaxIterations: DefaultMaxIterations,
	}
}

func (t *DispatchTool) Execute(ctx context.Context, input []byte) (tool.Result, error) {
	if !t.breaker.Allow() {
		return tool.Result{Error: "circuit breaker open: agent_dispatch temporarily unavailable"}, nil
	}

	var in dispatchToolInput
	if err := json.Unmarshal(input, &in); err != nil {
		t.breaker.RecordFailure()
		return tool.Result{Error: "invalid input: " + err.Error()}, nil
	}
	if in.Task == "" {
		t.breaker.RecordFailure()
		return tool.Result{Error: "task is required"}, nil
	}

	var spec *AgentSpec
	if in.Agent != "" && t.agents != nil {
		if registered, ok := t.agents.Get(in.Agent); ok {
			spec = registered
		}
	}
	if spec == nil {
		spec = buildDispatchSpec(in)
	}

	var parentID string
	var parentDepth int
	if pa := AgentFromContext(ctx); pa != nil {
		parentID = pa.AgentID()
	}
	if sc := SubagentContextFromCtx(ctx); sc != nil {
		parentDepth = sc.Depth
	}

	result, err := t.manager.Spawn(ctx, SpawnRequest{
		Spec:            spec,
		Task:            in.Task,
		ParentID:        parentID,
		ParentDepth:     parentDepth,
		ParentSessionID: tool.SessionIDFromContext(ctx),
	})
	if err != nil {
		t.breaker.RecordFailure()
		return tool.Result{Error: "dispatch error: " + err.Error()}, nil
	}
	if result.Error != "" {
		t.breaker.RecordFailure()
		return tool.Result{Error: result.Error}, nil
	}
	t.breaker.RecordSuccess()

	out := result.Summary
	if out == "" {
		out = result.Output
	}
	return tool.Result{Output: out}, nil
}
