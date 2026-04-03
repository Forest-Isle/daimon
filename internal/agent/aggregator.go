package agent

import (
	"context"
	"fmt"
	"strings"
)

// Aggregator synthesizes results from multiple sub-agent executions into a final answer.
type Aggregator struct {
	provider Provider
}

// NewAggregator creates a new Aggregator.
func NewAggregator(provider Provider) *Aggregator {
	return &Aggregator{provider: provider}
}

// Aggregate takes a TaskContext and TaskPlan, collects all sub-agent outputs,
// and uses an LLM call to synthesize them into a coherent final answer.
func (a *Aggregator) Aggregate(ctx context.Context, taskCtx *TaskContext, plan *TaskPlan) (string, error) {
	// Collect all agent_* subtask results
	var agentResults []SubAgentResult
	for _, st := range plan.SubTasks {
		if !strings.HasPrefix(st.ToolName, "agent_") {
			continue
		}
		if result, ok := taskCtx.GetResult(st.ID); ok {
			agentResults = append(agentResults, result)
		}
	}

	if len(agentResults) == 0 {
		return "", fmt.Errorf("no sub-agent results to aggregate")
	}

	// Build aggregation prompt
	var sb strings.Builder
	sb.WriteString("You are synthesizing results from multiple specialized agents.\n\n")
	_, _ = fmt.Fprintf(&sb, "Original Goal: %s\n\n", taskCtx.Goal)
	sb.WriteString("Agent Outputs:\n\n")

	for i, result := range agentResults {
		_, _ = fmt.Fprintf(&sb, "### Agent %d: %s\n\n", i+1, result.AgentName)
		if result.Error != "" {
			_, _ = fmt.Fprintf(&sb, "Error: %s\n\n", result.Error)
		} else {
			sb.WriteString(result.Output)
			sb.WriteString("\n\n")
		}
	}

	sb.WriteString("---\n\n")
	sb.WriteString("Synthesize the above outputs into a single, coherent final answer that addresses the original goal. ")
	sb.WriteString("Integrate insights from all agents, resolve any conflicts, and provide a comprehensive response.")

	req := CompletionRequest{
		Model:     "claude-sonnet-4-20250514", // Use a capable model for synthesis
		System:    "You are a synthesis agent that combines multiple perspectives into a unified answer.",
		Messages:  []CompletionMessage{{Role: "user", Content: sb.String()}},
		MaxTokens: 2048,
	}

	resp, err := a.provider.Complete(ctx, req)
	if err != nil {
		return "", fmt.Errorf("aggregation llm call: %w", err)
	}

	return resp.Text, nil
}
