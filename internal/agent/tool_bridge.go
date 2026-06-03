package agent

import (
	"context"
	"fmt"

	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// ToolExecutor adapts the Agent's tool execution pipeline for use by plan_task.
// It implements tool.SingleToolExecutor, breaking the circular dependency
// between the tool and agent packages.
type ToolExecutor struct {
	Agent *Agent
}

// Execute runs a single tool through the agent's interceptor chain (permission,
// hook, sandbox) and returns its output.
func (e *ToolExecutor) Execute(ctx context.Context, toolName, input string) (string, error) {
	if e.Agent == nil {
		return "", fmt.Errorf("ToolExecutor: Agent is nil")
	}

	call := &tool.ToolCall{ToolName: toolName, Input: input}

	t, err := e.Agent.deps.Core.Tools.Get(call.ToolName)
	if err != nil {
		return "", fmt.Errorf("tool not found: %s: %w", call.ToolName, err)
	}

	result, err := e.Agent.deps.Security.Interceptor.Execute(ctx, call,
		func(ctx context.Context, call *tool.ToolCall) (*tool.ToolResult, error) {
			r, execErr := t.Execute(ctx, []byte(call.Input))
			if execErr != nil {
				return &tool.ToolResult{Error: execErr.Error()}, nil
			}
			return &tool.ToolResult{Output: r.Output}, nil
		})
	if err != nil {
		return "", err
	}
	if result.Error != "" {
		return "", fmt.Errorf("%s", result.Error)
	}
	return result.Output, nil
}
