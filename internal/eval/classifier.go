package eval

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
)

// FailureCategory classifies the root cause of a task failure.
type FailureCategory string

const (
	FailPlanningError    FailureCategory = "planning_error"
	FailToolMisuse       FailureCategory = "tool_misuse"
	FailToolMissing      FailureCategory = "tool_missing"
	FailErrorNoRecovery  FailureCategory = "error_no_recovery"
	FailErrorLoopRetry   FailureCategory = "error_loop_retry"
	FailHallucination    FailureCategory = "hallucination"
	FailIncompleteAnswer FailureCategory = "incomplete_answer"
	FailWrongAnswer      FailureCategory = "wrong_answer"
	FailTimeout          FailureCategory = "timeout"
	FailContextLost      FailureCategory = "context_lost"
	FailOverEngineering  FailureCategory = "over_engineering"
	FailUnknown          FailureCategory = "unknown"
)

// AllFailureCategories returns all recognized failure categories.
func AllFailureCategories() []FailureCategory {
	return []FailureCategory{
		FailPlanningError, FailToolMisuse, FailToolMissing,
		FailErrorNoRecovery, FailErrorLoopRetry, FailHallucination,
		FailIncompleteAnswer, FailWrongAnswer, FailTimeout,
		FailContextLost, FailOverEngineering,
	}
}

// FailureClassifier categorizes evaluation failures using a two-stage approach:
// rule-based heuristics first, then optional LLM classification for ambiguous cases.
type FailureClassifier struct {
	provider    agent.Provider // nil = rule-only mode
	maxDuration time.Duration  // threshold for timeout classification
}

// NewFailureClassifier creates a classifier. provider may be nil for rule-only mode.
func NewFailureClassifier(provider agent.Provider, maxDuration time.Duration) *FailureClassifier {
	if maxDuration == 0 {
		maxDuration = 5 * time.Minute
	}
	return &FailureClassifier{provider: provider, maxDuration: maxDuration}
}

// Classify determines the failure category for a failed result.
// Successful results return an empty string.
func (c *FailureClassifier) Classify(ctx context.Context, task TaskCase, result *EvalResult) FailureCategory {
	if result.Success && result.FinalScore >= 0.8 {
		return ""
	}

	if cat := c.classifyByRules(task, result); cat != FailUnknown {
		return cat
	}

	if c.provider != nil {
		if cat := c.classifyByLLM(ctx, task, result); cat != "" {
			return cat
		}
	}

	return FailUnknown
}

// ClassifyAll classifies all failed results in a suite.
func (c *FailureClassifier) ClassifyAll(ctx context.Context, tasks []TaskCase, results []EvalResult) []EvalResult {
	taskMap := make(map[string]TaskCase, len(tasks))
	for _, t := range tasks {
		taskMap[t.ID] = t
	}

	classified := make([]EvalResult, len(results))
	copy(classified, results)

	for i := range classified {
		if classified[i].FailureCategory != "" {
			continue
		}
		task, ok := taskMap[classified[i].TaskID]
		if !ok {
			continue
		}
		cat := c.Classify(ctx, task, &classified[i])
		if cat != "" {
			classified[i].FailureCategory = string(cat)
		}
	}

	return classified
}

func (c *FailureClassifier) classifyByRules(task TaskCase, result *EvalResult) FailureCategory {
	if result.Duration > c.maxDuration {
		return FailTimeout
	}

	if result.ReplanCount > 3 {
		return FailErrorLoopRetry
	}

	if result.VerifyResult != nil {
		for _, check := range result.VerifyResult.Checks {
			if !check.Passed && strings.Contains(check.Name, "must_not_contain") {
				return FailHallucination
			}
		}
	}

	if result.JudgeResult != nil {
		for _, w := range result.JudgeResult.Weaknesses {
			wl := strings.ToLower(w)
			if strings.Contains(wl, "hallucin") {
				return FailHallucination
			}
			if strings.Contains(wl, "incomplete") {
				return FailIncompleteAnswer
			}
			if strings.Contains(wl, "wrong") || strings.Contains(wl, "incorrect") {
				return FailWrongAnswer
			}
		}
	}

	if len(task.ExpectTools) > 0 && len(result.ToolsUsed) > 0 {
		expectSet := make(map[string]bool)
		for _, t := range task.ExpectTools {
			expectSet[t] = true
		}
		usedSet := make(map[string]bool)
		for _, t := range result.ToolsUsed {
			usedSet[t] = true
		}

		missingExpected := false
		for _, t := range task.ExpectTools {
			if !usedSet[t] {
				missingExpected = true
				break
			}
		}
		if missingExpected {
			return FailToolMisuse
		}

		if len(result.ToolsUsed) > len(task.ExpectTools)*3 {
			return FailOverEngineering
		}
	}

	if result.Error != "" && result.ReplanCount == 0 {
		return FailErrorNoRecovery
	}

	if result.VerifyResult != nil && !result.VerifyResult.Passed && result.VerifyResult.Score < 0.5 {
		return FailWrongAnswer
	}

	if result.JudgeResult != nil && result.JudgeResult.Overall < 0.4 {
		return FailIncompleteAnswer
	}

	if (task.Complexity == "complex" || task.Dimension == DimPlanning) && result.FinalScore < 0.5 {
		return FailPlanningError
	}

	return FailUnknown
}

func (c *FailureClassifier) classifyByLLM(ctx context.Context, task TaskCase, result *EvalResult) FailureCategory {
	prompt := fmt.Sprintf(`Classify the failure category of this agent task execution.

## Task
ID: %s
Goal: %s
Dimension: %s
Expected Tools: %v

## Result
Success: %v
FinalScore: %.2f
Error: %s
Tools Used: %v
Replan Count: %d
Agent Output (truncated): %.500s

## Categories (pick exactly one)
- planning_error: Failed to decompose or sequence the task properly
- tool_misuse: Used wrong tool for the job
- tool_missing: Needed a tool that wasn't available
- error_no_recovery: Encountered error but didn't attempt recovery
- error_loop_retry: Got stuck in retry loop without progress
- hallucination: Generated false information
- incomplete_answer: Answer lacks required details
- wrong_answer: Produced factually incorrect result
- timeout: Took too long to complete
- context_lost: Lost track of conversation context
- over_engineering: Used excessive complexity for a simple task

Respond with ONLY the category name, nothing else.`,
		task.ID, task.Goal, task.Dimension, task.ExpectTools,
		result.Success, result.FinalScore, result.Error,
		result.ToolsUsed, result.ReplanCount, result.AgentOutput)

	resp, err := c.provider.Complete(ctx, agent.CompletionRequest{
		System:    "You are a failure classification expert. Respond with exactly one category name.",
		Messages:  []agent.CompletionMessage{{Role: "user", Content: prompt}},
		MaxTokens: 50,
	})
	if err != nil {
		slog.Warn("classifier: LLM call failed", "err", err)
		return ""
	}

	category := FailureCategory(strings.TrimSpace(resp.Text))

	for _, valid := range AllFailureCategories() {
		if category == valid {
			return category
		}
	}

	slog.Warn("classifier: LLM returned unknown category", "category", resp.Text)
	return ""
}
