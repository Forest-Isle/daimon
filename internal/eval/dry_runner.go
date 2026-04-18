package eval

import (
	"context"
	"time"
)

// DryRunner simulates task execution without calling a real LLM.
// It always succeeds, making it useful for testing the eval harness itself
// and for verifying task definitions. For real evaluation, wire a
// CognitiveAgentRunner through the gateway.
type DryRunner struct{}

func (d *DryRunner) RunTask(_ context.Context, task TaskCase) (*EvalResult, error) {
	start := time.Now()

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
		Timestamp:         time.Now(),
	}

	return result, nil
}
