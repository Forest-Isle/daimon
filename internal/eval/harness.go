package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/cogmetrics"
)

// TaskCase defines a single evaluation task with success criteria.
type TaskCase struct {
	ID          string   `json:"id"          yaml:"id"`
	Goal        string   `json:"goal"        yaml:"goal"`
	Complexity  string   `json:"complexity"  yaml:"complexity"`
	Tags        []string `json:"tags,omitempty"         yaml:"tags,omitempty"`
	ExpectTools []string `json:"expect_tools,omitempty" yaml:"expect_tools,omitempty"`

	// SuccessFunc is an optional programmatic check run after execution.
	// When nil the result relies on the agent's own reflection.
	SuccessFunc func(result *EvalResult) bool `json:"-" yaml:"-"`

	Dimension    Dimension    `json:"dimension,omitempty"     yaml:"dimension,omitempty"`
	VerifyMethod VerifyMethod `json:"verify_method,omitempty" yaml:"verify_method,omitempty"`
	Reference    *Reference   `json:"reference,omitempty"     yaml:"reference,omitempty"`
	Rubric       *Rubric      `json:"rubric,omitempty"        yaml:"rubric,omitempty"`
	SetupFunc    func() error `json:"-" yaml:"-"`
	CleanupFunc  func() error `json:"-" yaml:"-"`
}

type Reference struct {
	Answer         string      `json:"answer,omitempty"           yaml:"answer,omitempty"`
	MustContain    []string    `json:"must_contain,omitempty"     yaml:"must_contain,omitempty"`
	MustNotContain []string    `json:"must_not_contain,omitempty" yaml:"must_not_contain,omitempty"`
	FileChecks     []FileCheck `json:"file_checks,omitempty"      yaml:"file_checks,omitempty"`
	ExitCode       *int        `json:"exit_code,omitempty"        yaml:"exit_code,omitempty"`
}

type FileCheck struct {
	Path        string `json:"path"                  yaml:"path"`
	MustExist   bool   `json:"must_exist"            yaml:"must_exist"`
	Contains    string `json:"contains,omitempty"    yaml:"contains,omitempty"`
	NotContains string `json:"not_contains,omitempty" yaml:"not_contains,omitempty"`
}

type Rubric struct {
	Criteria []JudgeCriterion `json:"criteria"`
}

type JudgeCriterion struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Weight      float64 `json:"weight"`
}

// CompressionEvent records a single context compression occurrence during task execution.
type CompressionEvent struct {
	Reason    string  `json:"reason"`
	LayersRun int     `json:"layers_run"`
	BeforePct float64 `json:"before_pct"`
	AfterPct  float64 `json:"after_pct"`
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

	Dimension         Dimension          `json:"dimension,omitempty"`
	AgentOutput       string             `json:"agent_output,omitempty"`
	VerifyResult      *VerifyResult      `json:"verify_result,omitempty"`
	JudgeResult       *JudgeResult       `json:"judge_result,omitempty"`
	FinalScore        float64            `json:"final_score,omitempty"`
	FailureCategory   string             `json:"failure_category,omitempty"`
	CompressionCount  int                `json:"compression_count,omitempty"`
	CompressionEvents []CompressionEvent `json:"compression_events,omitempty"`
}

type VerifyResult struct {
	Passed bool          `json:"passed"`
	Checks []CheckResult `json:"checks"`
	Score  float64       `json:"score"`
}

type CheckResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

type JudgeResult struct {
	Scores     map[string]float64 `json:"scores"`
	Overall    float64            `json:"overall"`
	Reasoning  string             `json:"reasoning"`
	Weaknesses []string           `json:"weaknesses,omitempty"`
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
	RunID     string        `json:"run_id"`
	Results   []EvalResult  `json:"results"`
	StartedAt time.Time     `json:"started_at"`
	Duration  time.Duration `json:"duration_ms"`

	EvoBefore *EvolutionSnapshot `json:"evo_before,omitempty"`
	EvoAfter  *EvolutionSnapshot `json:"evo_after,omitempty"`

	// CogHealth is a point-in-time snapshot of cognitive-metrics accumulated
	// across the suite. Populated when the runner has a cogmetrics.Collector
	// wired (i.e. evolution is enabled). Nil when evolution is disabled.
	CogHealth *cogmetrics.HealthReport `json:"cog_health,omitempty"`

	// FeatureState records which gateway features were enabled during this eval run.
	// Populated when running against a live cognitive agent. Used to detect
	// configuration differences when comparing two runs.
	FeatureState map[string]bool `json:"feature_state,omitempty"`
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

// CogHealthCaptor is optionally implemented by AgentRunner to capture a
// cognitive-metrics health report after a suite completes.
type CogHealthCaptor interface {
	CaptureCogHealth() *cogmetrics.HealthReport
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
	if chc, ok := runner.(CogHealthCaptor); ok {
		suite.CogHealth = chc.CaptureCogHealth()
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

// RunOptions configures optional behavior for RunSuiteWithOptions.
type RunOptions struct {
	Judge *LLMJudge
}

// RunSuiteWithOptions extends RunSuite with verification, judging, and setup/cleanup.
// Passing nil options is equivalent to calling RunSuite.
func RunSuiteWithOptions(ctx context.Context, runID string, tasks []TaskCase, runner AgentRunner, opts *RunOptions) (*SuiteResult, error) {
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

		if task.SetupFunc != nil {
			if err := task.SetupFunc(); err != nil {
				suite.Results = append(suite.Results, EvalResult{
					TaskID:    task.ID,
					Goal:      task.Goal,
					Error:     fmt.Sprintf("setup failed: %v", err),
					Dimension: DefaultDimension(task.Dimension),
					Timestamp: time.Now(),
				})
				continue
			}
		}

		result, err := runner.RunTask(ctx, task)

		if task.CleanupFunc != nil {
			_ = task.CleanupFunc()
		}

		if err != nil {
			suite.Results = append(suite.Results, EvalResult{
				TaskID:    task.ID,
				Goal:      task.Goal,
				Error:     err.Error(),
				Dimension: DefaultDimension(task.Dimension),
				Timestamp: time.Now(),
			})
			continue
		}

		result.Dimension = DefaultDimension(task.Dimension)

		if task.SuccessFunc != nil {
			result.Success = task.SuccessFunc(result)
		}

		agentOutput := result.AgentOutput

		var vr *VerifyResult
		if task.Reference != nil {
			vr = VerifyReference(task, agentOutput)
			result.VerifyResult = vr
		}

		var jr *JudgeResult
		if opts != nil && opts.Judge != nil && task.Rubric != nil &&
			(task.VerifyMethod == VerifyLLMJudge || task.VerifyMethod == VerifyHybrid) {
			var judgeErr error
			jr, judgeErr = opts.Judge.Judge(ctx, task, agentOutput, result.ToolsUsed)
			if judgeErr != nil {
				slog.Warn("judge failed for task", "task", task.ID, "err", judgeErr)
			} else {
				result.JudgeResult = jr
			}
		}

		result.FinalScore = ComputeFinalScore(task.VerifyMethod, vr, jr, result.AssertionPassRate)

		suite.Results = append(suite.Results, *result)

		fmt.Printf("  [%d/%d] %s — %s (%.1fs, score=%.2f)\n",
			i+1, len(tasks), task.ID, statusLabel(result.Success),
			result.Duration.Seconds(), result.FinalScore)
	}

	if sc, ok := runner.(SnapshotCaptor); ok {
		suite.EvoAfter = sc.CaptureSnapshot()
	}
	if chc, ok := runner.(CogHealthCaptor); ok {
		suite.CogHealth = chc.CaptureCogHealth()
	}

	suite.Duration = time.Since(suite.StartedAt)
	return suite, nil
}

// ComputeFinalScore synthesizes a single score from verification and judge results.
func ComputeFinalScore(method VerifyMethod, vr *VerifyResult, jr *JudgeResult, assertionPassRate float64) float64 {
	switch method {
	case VerifyDeterministic:
		if vr != nil {
			return vr.Score
		}
		return assertionPassRate
	case VerifyLLMJudge:
		if jr != nil {
			return jr.Overall
		}
		return 0.5
	case VerifyHybrid:
		vs := assertionPassRate
		if vr != nil {
			vs = vr.Score
		}
		js := 0.5
		if jr != nil {
			js = jr.Overall
		}
		return 0.5*vs + 0.5*js
	default:
		return assertionPassRate
	}
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
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create output dir: %w", err)
		}
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

// IterationPoint captures metrics for a single iteration in a longitudinal run,
// combining the eval summary with evolution subsystem state.
type IterationPoint struct {
	Iteration       int          `json:"iteration"`
	RunID           string       `json:"run_id"`
	Timestamp       time.Time    `json:"timestamp"`
	Summary         SuiteSummary `json:"summary"`
	StrategyVersion int          `json:"strategy_version"`
	PreferenceCount int          `json:"preference_count"`
	SkillDraftCount int          `json:"skill_draft_count"`
	TrajectoryCount int          `json:"trajectory_count"`
}

// LongitudinalReport captures the full time series of a longitudinal evaluation
// run, including per-iteration metrics and first-vs-last comparison deltas.
type LongitudinalReport struct {
	Iterations  []IterationPoint `json:"iterations"`
	First       SuiteSummary     `json:"first"`
	Last        SuiteSummary     `json:"last"`
	Deltas      ComparisonDelta  `json:"deltas"`
	GeneratedAt time.Time        `json:"generated_at"`
}

// NewLongitudinalReport creates a report from a series of iteration points.
// Computes first-vs-last deltas automatically.
func NewLongitudinalReport(points []IterationPoint) *LongitudinalReport {
	if len(points) == 0 {
		return &LongitudinalReport{GeneratedAt: time.Now()}
	}

	first := points[0].Summary
	last := points[len(points)-1].Summary

	return &LongitudinalReport{
		Iterations: points,
		First:      first,
		Last:       last,
		Deltas: ComparisonDelta{
			SuccessRateDelta:       last.SuccessRate - first.SuccessRate,
			AvgAssertPassRateDelta: last.AvgAssertionPassRate - first.AvgAssertionPassRate,
			AvgConfidenceDelta:     last.AvgConfidence - first.AvgConfidence,
			AvgReplanCountDelta:    last.AvgReplanCount - first.AvgReplanCount,
			DurationDelta:          last.Duration - first.Duration,
		},
		GeneratedAt: time.Now(),
	}
}

// SaveJSON writes the longitudinal report to a JSON file.
func (r *LongitudinalReport) SaveJSON(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal longitudinal report: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadLongitudinalReport reads a longitudinal report from a JSON file.
func LoadLongitudinalReport(path string) (*LongitudinalReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read longitudinal report: %w", err)
	}
	var report LongitudinalReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("unmarshal longitudinal report: %w", err)
	}
	return &report, nil
}
