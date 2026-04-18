package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// TaskCase defines a single evaluation task with success criteria.
type TaskCase struct {
	ID          string   `json:"id"`
	Goal        string   `json:"goal"`
	Complexity  string   `json:"complexity"`
	Tags        []string `json:"tags,omitempty"`
	ExpectTools []string `json:"expect_tools,omitempty"`

	// SuccessFunc is an optional programmatic check run after execution.
	// When nil the result relies on the agent's own reflection.
	SuccessFunc func(result *EvalResult) bool `json:"-"`
}

// EvalResult captures the outcome of running one TaskCase.
type EvalResult struct {
	TaskID            string        `json:"task_id"`
	Goal              string        `json:"goal"`
	Complexity        string        `json:"complexity"`
	Success           bool          `json:"success"`
	Duration          time.Duration `json:"duration_ms"`
	ToolsUsed         []string      `json:"tools_used"`
	ReplanCount       int           `json:"replan_count"`
	AssertionTotal    int           `json:"assertion_total"`
	AssertionPassed   int           `json:"assertion_passed"`
	AssertionPassRate float64       `json:"assertion_pass_rate"`
	Confidence        float64       `json:"confidence"`
	Error             string        `json:"error,omitempty"`
	Timestamp         time.Time     `json:"timestamp"`
}

// EvolutionSnapshot captures evolution subsystem state at a point in time.
type EvolutionSnapshot struct {
	PreferenceCount int `json:"preference_count"`
	StrategyVersion int `json:"strategy_version"`
	SkillDraftCount int `json:"skill_draft_count"`
	TrajectoryCount int `json:"trajectory_count"`
}

// SuiteResult aggregates results across a full evaluation run.
type SuiteResult struct {
	RunID     string       `json:"run_id"`
	Results   []EvalResult `json:"results"`
	StartedAt time.Time    `json:"started_at"`
	Duration  time.Duration `json:"duration_ms"`

	EvoBefore *EvolutionSnapshot `json:"evo_before,omitempty"`
	EvoAfter  *EvolutionSnapshot `json:"evo_after,omitempty"`
}

// AgentRunner abstracts the cognitive agent interface for evaluation.
// This allows the harness to work with both real and mock agents.
type AgentRunner interface {
	// RunTask executes a single task and returns an EvalResult.
	RunTask(ctx context.Context, task TaskCase) (*EvalResult, error)
}

// SnapshotCaptor is optionally implemented by AgentRunner to capture evolution state.
type SnapshotCaptor interface {
	CaptureSnapshot() *EvolutionSnapshot
}

// RunSuite executes all tasks against the given runner and collects results.
func RunSuite(ctx context.Context, runID string, tasks []TaskCase, runner AgentRunner) (*SuiteResult, error) {
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no tasks to evaluate")
	}

	suite := &SuiteResult{
		RunID:     runID,
		Results:   make([]EvalResult, 0, len(tasks)),
		StartedAt: time.Now(),
	}

	if sc, ok := runner.(SnapshotCaptor); ok {
		suite.EvoBefore = sc.CaptureSnapshot()
	}

	for i, task := range tasks {
		select {
		case <-ctx.Done():
			return suite, ctx.Err()
		default:
		}

		result, err := runner.RunTask(ctx, task)
		if err != nil {
			suite.Results = append(suite.Results, EvalResult{
				TaskID:    task.ID,
				Goal:      task.Goal,
				Error:     err.Error(),
				Timestamp: time.Now(),
			})
			continue
		}

		if task.SuccessFunc != nil {
			result.Success = task.SuccessFunc(result)
		}

		suite.Results = append(suite.Results, *result)

		fmt.Printf("  [%d/%d] %s — %s (%.1fs)\n",
			i+1, len(tasks), task.ID, statusLabel(result.Success), result.Duration.Seconds())
	}

	if sc, ok := runner.(SnapshotCaptor); ok {
		suite.EvoAfter = sc.CaptureSnapshot()
	}

	suite.Duration = time.Since(suite.StartedAt)
	return suite, nil
}

func statusLabel(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// Summary computes aggregate statistics from a suite result.
func (s *SuiteResult) Summary() SuiteSummary {
	sum := SuiteSummary{
		RunID:      s.RunID,
		TotalTasks: len(s.Results),
		Duration:   s.Duration,
	}

	var totalAssertRate float64
	var totalConfidence float64
	var totalReplan int

	for _, r := range s.Results {
		if r.Success {
			sum.Passed++
		} else {
			sum.Failed++
		}
		if r.Error != "" {
			sum.Errors++
		}
		totalAssertRate += r.AssertionPassRate
		totalConfidence += r.Confidence
		totalReplan += r.ReplanCount
	}

	n := float64(len(s.Results))
	if n > 0 {
		sum.SuccessRate = float64(sum.Passed) / n
		sum.AvgAssertionPassRate = totalAssertRate / n
		sum.AvgConfidence = totalConfidence / n
		sum.AvgReplanCount = float64(totalReplan) / n
	}

	return sum
}

// SuiteSummary holds aggregate metrics for an evaluation run.
type SuiteSummary struct {
	RunID              string        `json:"run_id"`
	TotalTasks         int           `json:"total_tasks"`
	Passed             int           `json:"passed"`
	Failed             int           `json:"failed"`
	Errors             int           `json:"errors"`
	SuccessRate        float64       `json:"success_rate"`
	AvgAssertionPassRate float64     `json:"avg_assertion_pass_rate"`
	AvgConfidence      float64       `json:"avg_confidence"`
	AvgReplanCount     float64       `json:"avg_replan_count"`
	Duration           time.Duration `json:"duration_ms"`
}

// SaveJSON writes the suite result to a JSON file.
func (s *SuiteResult) SaveJSON(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal suite result: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadSuiteResult reads a suite result from a JSON file.
func LoadSuiteResult(path string) (*SuiteResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read suite result: %w", err)
	}
	var result SuiteResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshal suite result: %w", err)
	}
	return &result, nil
}
