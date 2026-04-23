package eval

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// DryRunner simulates task execution without calling a real LLM.
// It always succeeds, making it useful for testing the eval harness itself
// and for verifying task definitions. For real evaluation, wire a
// CognitiveAgentRunner through the gateway.
type DryRunner struct{}

func (d *DryRunner) RunTask(ctx context.Context, task TaskCase) (*EvalResult, error) {
	if task.ID == TaskIDSkillEvolutionDraftQuality {
		return RunSkillEvolutionDimensionCheck(ctx, task)
	}

	start := time.Now()

	agentOutput := fmt.Sprintf("Dry-run result for task %s: all checks passed.", task.ID)
	if task.Reference != nil && task.Reference.Answer != "" {
		agentOutput = task.Reference.Answer
	} else if task.Reference != nil && len(task.Reference.MustContain) > 0 {
		agentOutput = strings.Join(task.Reference.MustContain, "\n")
	}

	result := &EvalResult{
		TaskID:            task.ID,
		Goal:              task.Goal,
		Complexity:        task.Complexity,
		Success:           true,
		Duration:          time.Since(start),
		ToolsUsed:         task.ExpectTools,
		ReplanCount:       0,
		AssertionTotal:    len(task.ExpectTools),
		AssertionPassed:   len(task.ExpectTools),
		AssertionPassRate: 1.0,
		Confidence:        0.95,
		AgentOutput:       agentOutput,
		Timestamp:         time.Now(),
	}

	return result, nil
}
