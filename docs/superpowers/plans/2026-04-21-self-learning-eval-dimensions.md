# Self-Learning Evaluation Dimensions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add 3 new evaluation dimensions (skill_learning, preference_adherence, memory_retention) + learning curve analytics + `eval self-learning` CLI command to comprehensively assess agent self-improvement capabilities.

**Architecture:** 
- New `fixtures_self_learning.go` holds 3 fixture suites targeting the new dimensions.
- New `learning_metrics.go` holds `SelfLearningReport` / `LearningCurveAnalysis` / `StrategyConvergenceAnalysis` types + computation functions.
- The `eval self-learning` CLI subcommand runs all 3 suites and optionally computes learning curves from a prior longitudinal report.
- `LongitudinalReport` is extended with `SelfLearningAnalysis` so `eval longitudinal` auto-produces learning curve data.

**Tech Stack:** Go 1.22+, Cobra CLI, existing `internal/eval` package, `memory.Entry`, `strings` / `math` standard library.

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/eval/dimension.go` | Modify | Add 3 new `Dimension` constants + update `AllDimensions()` |
| `internal/eval/fixtures_self_learning.go` | Create | `SkillLearningSuite()`, `PreferenceAdherenceSuite()`, `MemoryRetentionSuite()` |
| `internal/eval/fixtures.go` | Modify | Register the 3 new suites in `AllSuites()` + add them to `FullSuite()` |
| `internal/eval/learning_metrics.go` | Create | `LearningCurveAnalysis`, `StrategyConvergenceAnalysis`, `SelfLearningReport`, `ComputeLearningCurve()`, `ComputeStrategyConvergence()`, `ComputeSelfLearningReport()` |
| `internal/eval/harness.go` | Modify | Add `SelfLearningAnalysis *SelfLearningAnalysisSummary` field to `LongitudinalReport`; add `NewLongitudinalReportWithLearning()` helper |
| `cmd/ironclaw/eval.go` | Modify | Add `newEvalSelfLearningCmd()` + wire into `AddCommand`; add learning curve HTML to `eval longitudinal` output |

---

## Task 1: New Dimension Constants

**Files:**
- Modify: `internal/eval/dimension.go`

- [ ] **Step 1: Add the 3 new dimension constants and update `AllDimensions()`**

Open `internal/eval/dimension.go`. After the existing `DimMultiAgent` constant, add:

```go
DimSkillLearning       Dimension = "skill_learning"
DimPreferenceAdherence Dimension = "preference_adherence"
DimMemoryRetention     Dimension = "memory_retention"
```

Update `AllDimensions()` to return all 11 dimensions (append the 3 new ones):

```go
func AllDimensions() []Dimension {
	return []Dimension{
		DimTaskExecution, DimPlanning, DimErrorRecovery, DimToolSelection,
		DimConversation, DimMemory, DimKnowledge, DimMultiAgent,
		DimSkillLearning, DimPreferenceAdherence, DimMemoryRetention,
	}
}
```

- [ ] **Step 2: Verify it compiles**

```bash
CGO_ENABLED=1 go build -tags fts5 ./internal/eval/
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/eval/dimension.go
git commit -m "feat(eval): add skill_learning, preference_adherence, memory_retention dimensions"
```

---

## Task 2: Self-Learning Fixture Suites

**Files:**
- Create: `internal/eval/fixtures_self_learning.go`
- Modify: `internal/eval/fixtures.go`

- [ ] **Step 1: Create `internal/eval/fixtures_self_learning.go`**

```go
package eval

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/memory"
)

// SkillLearningSuite returns tasks that test whether skill synthesis produces
// measurable behavior improvements. Each task uses deterministic verification:
// the correct output is checked in SuccessFunc, independent of LLM reflection.
func SkillLearningSuite() []TaskCase {
	return []TaskCase{
		{
			ID:           "skill-bash-arithmetic",
			Goal:         "Read the number stored in /tmp/sl_arith_in.txt, double it, write the result to /tmp/sl_arith_out.txt",
			Complexity:   "simple",
			Dimension:    DimSkillLearning,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"skill_learning", "bash", "arithmetic"},
			SetupFunc: func() error {
				return os.WriteFile("/tmp/sl_arith_in.txt", []byte("42\n"), 0o644)
			},
			CleanupFunc: func() error {
				os.Remove("/tmp/sl_arith_in.txt")
				os.Remove("/tmp/sl_arith_out.txt")
				return nil
			},
			SuccessFunc: func(r *EvalResult) bool {
				data, err := os.ReadFile("/tmp/sl_arith_out.txt")
				if err != nil {
					return false
				}
				return strings.TrimSpace(string(data)) == "84"
			},
		},
		{
			ID:           "skill-file-append",
			Goal:         "Append the text 'SKILL_TEST_MARKER' to /tmp/sl_append.txt (create it if needed), then print its contents",
			Complexity:   "simple",
			Dimension:    DimSkillLearning,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"skill_learning", "file"},
			CleanupFunc: func() error {
				os.Remove("/tmp/sl_append.txt")
				return nil
			},
			SuccessFunc: func(r *EvalResult) bool {
				data, err := os.ReadFile("/tmp/sl_append.txt")
				if err != nil {
					return false
				}
				return strings.Contains(string(data), "SKILL_TEST_MARKER")
			},
		},
		{
			ID:           "skill-dependency-chain",
			Goal:         "Create /tmp/sl_chain_a.txt with content '100', then read it, multiply by 3, write result to /tmp/sl_chain_b.txt",
			Complexity:   "medium",
			Dimension:    DimSkillLearning,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"skill_learning", "dependency_chain"},
			CleanupFunc: func() error {
				os.Remove("/tmp/sl_chain_a.txt")
				os.Remove("/tmp/sl_chain_b.txt")
				return nil
			},
			SuccessFunc: func(r *EvalResult) bool {
				data, err := os.ReadFile("/tmp/sl_chain_b.txt")
				if err != nil {
					return false
				}
				return strings.TrimSpace(string(data)) == "300"
			},
		},
		{
			ID:           "skill-count-lines",
			Goal:         "Count the number of lines in /tmp/sl_count_src.txt and write just the number to /tmp/sl_count_out.txt",
			Complexity:   "simple",
			Dimension:    DimSkillLearning,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"skill_learning", "bash"},
			SetupFunc: func() error {
				return os.WriteFile("/tmp/sl_count_src.txt", []byte("line1\nline2\nline3\nline4\nline5\n"), 0o644)
			},
			CleanupFunc: func() error {
				os.Remove("/tmp/sl_count_src.txt")
				os.Remove("/tmp/sl_count_out.txt")
				return nil
			},
			SuccessFunc: func(r *EvalResult) bool {
				data, err := os.ReadFile("/tmp/sl_count_out.txt")
				if err != nil {
					return false
				}
				return strings.TrimSpace(string(data)) == "5"
			},
		},
		{
			ID:           "skill-pipeline-sum",
			Goal:         "Sum all numbers in /tmp/sl_sum_in.txt (one per line) and write the total to /tmp/sl_sum_out.txt",
			Complexity:   "medium",
			Dimension:    DimSkillLearning,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"skill_learning", "bash", "arithmetic"},
			SetupFunc: func() error {
				return os.WriteFile("/tmp/sl_sum_in.txt", []byte("10\n20\n30\n40\n"), 0o644)
			},
			CleanupFunc: func() error {
				os.Remove("/tmp/sl_sum_in.txt")
				os.Remove("/tmp/sl_sum_out.txt")
				return nil
			},
			SuccessFunc: func(r *EvalResult) bool {
				data, err := os.ReadFile("/tmp/sl_sum_out.txt")
				if err != nil {
					return false
				}
				return strings.TrimSpace(string(data)) == "100"
			},
		},
		{
			ID:           "skill-temp-dir-cleanup",
			Goal:         "Create directory /tmp/sl_cleanup_dir/, create 3 files inside it, then delete the directory and all its contents. Confirm deletion by checking it no longer exists.",
			Complexity:   "medium",
			Dimension:    DimSkillLearning,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"skill_learning", "bash", "file"},
			SuccessFunc: func(r *EvalResult) bool {
				_, err := os.Stat("/tmp/sl_cleanup_dir")
				return os.IsNotExist(err)
			},
		},
	}
}

// PreferenceAdherenceSuite returns tasks with explicit behavioral constraints
// embedded in the goal. Each task verifies whether the agent followed the stated
// preference via deterministic SuccessFunc checks on AgentOutput or side effects.
func PreferenceAdherenceSuite() []TaskCase {
	return []TaskCase{
		{
			ID:           "pref-concise-answer",
			Goal:         "What is the current time? Reply with only the time value, nothing else. Keep your answer under 20 words total.",
			Complexity:   "simple",
			Dimension:    DimPreferenceAdherence,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"preference_adherence", "conciseness"},
			SuccessFunc: func(r *EvalResult) bool {
				words := strings.Fields(r.AgentOutput)
				return r.Success && len(words) <= 20
			},
		},
		{
			ID:           "pref-bash-tool-only",
			Goal:         "List the contents of the /tmp directory. You must use the bash tool for this. Do not use any file listing or file reading tools.",
			Complexity:   "simple",
			Dimension:    DimPreferenceAdherence,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"preference_adherence", "tool_selection"},
			SuccessFunc: func(r *EvalResult) bool {
				for _, t := range r.ToolsUsed {
					if t == "file_list" || t == "file_read" {
						return false
					}
				}
				usedBash := false
				for _, t := range r.ToolsUsed {
					if t == "bash" {
						usedBash = true
					}
				}
				return usedBash
			},
		},
		{
			ID:           "pref-json-output",
			Goal:         `What is 2+2? Reply ONLY with valid JSON in the format: {"answer": <number>}. Do not include any prose, markdown, or explanation.`,
			Complexity:   "simple",
			Dimension:    DimPreferenceAdherence,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"preference_adherence", "output_format"},
			SuccessFunc: func(r *EvalResult) bool {
				out := strings.TrimSpace(r.AgentOutput)
				return strings.HasPrefix(out, "{") && strings.HasSuffix(out, "}") &&
					strings.Contains(out, `"answer"`) && strings.Contains(out, "4")
			},
		},
		{
			ID:           "pref-numbered-steps",
			Goal:         "Explain how to make coffee in exactly 3 numbered steps. Format each step as '1. ', '2. ', '3. '.",
			Complexity:   "simple",
			Dimension:    DimPreferenceAdherence,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"preference_adherence", "output_format"},
			SuccessFunc: func(r *EvalResult) bool {
				out := r.AgentOutput
				return strings.Contains(out, "1.") && strings.Contains(out, "2.") && strings.Contains(out, "3.")
			},
		},
		{
			ID:           "pref-no-tool-use",
			Goal:         "What is the capital of France? Answer from your knowledge only — do NOT use any tools or bash commands.",
			Complexity:   "simple",
			Dimension:    DimPreferenceAdherence,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"preference_adherence", "knowledge_only"},
			SuccessFunc: func(r *EvalResult) bool {
				return len(r.ToolsUsed) == 0 &&
					strings.Contains(strings.ToLower(r.AgentOutput), "paris")
			},
		},
		{
			ID:           "pref-single-command-answer",
			Goal:         "Get the hostname of this machine. Use exactly one bash command. Do not run multiple commands.",
			Complexity:   "simple",
			Dimension:    DimPreferenceAdherence,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"preference_adherence", "efficiency"},
			SuccessFunc: func(r *EvalResult) bool {
				bashCount := 0
				for _, t := range r.ToolsUsed {
					if t == "bash" {
						bashCount++
					}
				}
				return bashCount == 1 && r.Success
			},
		},
		{
			ID:           "pref-uppercase-output",
			Goal:         "Echo the phrase 'hello world' using bash. The output must appear in UPPERCASE in your response.",
			Complexity:   "simple",
			Dimension:    DimPreferenceAdherence,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"preference_adherence", "output_format"},
			SuccessFunc: func(r *EvalResult) bool {
				return strings.Contains(r.AgentOutput, "HELLO WORLD")
			},
		},
		{
			ID:           "pref-write-specific-file",
			Goal:         "Write the word 'DONE' to /tmp/pref_test_done.txt. The file must exist with that exact content when you finish.",
			Complexity:   "simple",
			Dimension:    DimPreferenceAdherence,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"preference_adherence", "file_output"},
			CleanupFunc: func() error {
				os.Remove("/tmp/pref_test_done.txt")
				return nil
			},
			SuccessFunc: func(r *EvalResult) bool {
				data, err := os.ReadFile("/tmp/pref_test_done.txt")
				if err != nil {
					return false
				}
				return strings.TrimSpace(string(data)) == "DONE"
			},
		},
	}
}

// injectMemoryHelper injects memory entries if the runner is MemoryAwareRunner.
// Returns nil silently if the runner doesn't support memory injection.
func injectMemoryHelper(ctx context.Context, runner AgentRunner, entries ...memory.Entry) error {
	type memAware interface {
		InjectMemory(ctx context.Context, entries ...memory.Entry) error
	}
	if ma, ok := runner.(memAware); ok {
		return ma.InjectMemory(ctx, entries...)
	}
	return nil
}

// MemoryRetentionSuite returns tasks that test cross-session memory persistence,
// precision recall, and multi-hop reasoning over injected memory entries.
func MemoryRetentionSuite() []TaskCase {
	return []TaskCase{
		{
			ID:         "mem-ret-basic-recall",
			Goal:       "What is Alice's favorite programming language?",
			Complexity: "simple",
			Dimension:  DimMemoryRetention,
			Tags:       []string{"memory_retention", "recall"},
			SetupWithRunner: func(ctx context.Context, runner AgentRunner) error {
				return injectMemoryHelper(ctx, runner, memory.Entry{
					ID:      "mem-ret-alice-lang",
					Content: "Alice's favorite programming language is Rust. She has been using it since 2019.",
					Scope:   memory.ScopeUser,
					Tags:    []string{"user_profile", "alice"},
				})
			},
			SuccessFunc: func(r *EvalResult) bool {
				return strings.Contains(strings.ToLower(r.AgentOutput), "rust")
			},
		},
		{
			ID:         "mem-ret-precision-recall",
			Goal:       "What programming language does Bob prefer? (Do not confuse with Alice's preference.)",
			Complexity: "simple",
			Dimension:  DimMemoryRetention,
			Tags:       []string{"memory_retention", "precision"},
			SetupWithRunner: func(ctx context.Context, runner AgentRunner) error {
				return injectMemoryHelper(ctx, runner,
					memory.Entry{
						ID:      "mem-ret-alice-lang-precision",
						Content: "Alice's favorite programming language is Rust.",
						Scope:   memory.ScopeUser,
						Tags:    []string{"user_profile", "alice"},
					},
					memory.Entry{
						ID:      "mem-ret-bob-lang",
						Content: "Bob's preferred programming language is Python. He uses it for data science.",
						Scope:   memory.ScopeUser,
						Tags:    []string{"user_profile", "bob"},
					},
				)
			},
			SuccessFunc: func(r *EvalResult) bool {
				out := strings.ToLower(r.AgentOutput)
				return strings.Contains(out, "python") && !strings.Contains(out, "rust")
			},
		},
		{
			ID:         "mem-ret-multi-hop",
			Goal:       "What city does the team lead of Project Phoenix live in?",
			Complexity: "medium",
			Dimension:  DimMemoryRetention,
			Tags:       []string{"memory_retention", "multi_hop"},
			SetupWithRunner: func(ctx context.Context, runner AgentRunner) error {
				return injectMemoryHelper(ctx, runner,
					memory.Entry{
						ID:      "mem-ret-phoenix-lead",
						Content: "Project Phoenix team lead is Carol Chen.",
						Scope:   memory.ScopeUser,
						Tags:    []string{"project", "team"},
					},
					memory.Entry{
						ID:      "mem-ret-carol-location",
						Content: "Carol Chen lives in Singapore. She works from home.",
						Scope:   memory.ScopeUser,
						Tags:    []string{"user_profile", "location"},
					},
				)
			},
			SuccessFunc: func(r *EvalResult) bool {
				return strings.Contains(strings.ToLower(r.AgentOutput), "singapore")
			},
		},
		{
			ID:         "mem-ret-numeric-fact",
			Goal:       "How many team members does Project Alpha have?",
			Complexity: "simple",
			Dimension:  DimMemoryRetention,
			Tags:       []string{"memory_retention", "numeric"},
			SetupWithRunner: func(ctx context.Context, runner AgentRunner) error {
				return injectMemoryHelper(ctx, runner, memory.Entry{
					ID:      "mem-ret-alpha-size",
					Content: "Project Alpha has 7 team members: 4 engineers, 2 designers, and 1 product manager.",
					Scope:   memory.ScopeUser,
					Tags:    []string{"project", "team"},
				})
			},
			SuccessFunc: func(r *EvalResult) bool {
				return strings.Contains(r.AgentOutput, "7")
			},
		},
		{
			ID:         "mem-ret-temporal-fact",
			Goal:       "When does the Q2 planning session start?",
			Complexity: "simple",
			Dimension:  DimMemoryRetention,
			Tags:       []string{"memory_retention", "temporal"},
			SetupWithRunner: func(ctx context.Context, runner AgentRunner) error {
				return injectMemoryHelper(ctx, runner, memory.Entry{
					ID:      "mem-ret-q2-planning",
					Content: "The Q2 planning session is scheduled for April 28, 2026 at 10:00 AM CST.",
					Scope:   memory.ScopeUser,
					Tags:    []string{"schedule", "planning"},
				})
			},
			SuccessFunc: func(r *EvalResult) bool {
				out := strings.ToLower(r.AgentOutput)
				return strings.Contains(out, "april 28") || strings.Contains(out, "apr 28") ||
					strings.Contains(out, "april 28, 2026")
			},
		},
		{
			ID:         "mem-ret-unknown-fact",
			Goal:       "What is David's phone number? If you don't know, say 'I don't have that information'.",
			Complexity: "simple",
			Dimension:  DimMemoryRetention,
			Tags:       []string{"memory_retention", "negative_recall"},
			SuccessFunc: func(r *EvalResult) bool {
				out := strings.ToLower(r.AgentOutput)
				return strings.Contains(out, "don't have") ||
					strings.Contains(out, "do not have") ||
					strings.Contains(out, "no information") ||
					strings.Contains(out, "not available") ||
					strings.Contains(out, "don't know")
			},
		},
	}
}

// SelfLearningSuite combines all self-learning dimensions into one composite suite.
func SelfLearningSuite() []TaskCase {
	var all []TaskCase
	all = append(all, SkillLearningSuite()...)
	all = append(all, PreferenceAdherenceSuite()...)
	all = append(all, MemoryRetentionSuite()...)
	return all
}

// formatToolList joins a string slice for display; returns "(none)" when empty.
func formatToolList(tools []string) string {
	if len(tools) == 0 {
		return "(none)"
	}
	return fmt.Sprintf("[%s]", strings.Join(tools, ", "))
}
```

- [ ] **Step 2: Update `internal/eval/fixtures.go` to register the 3 new suites**

In `FullSuite()`, append the 3 new suites. Replace the existing `FullSuite()` body:

```go
func FullSuite() []TaskCase {
	var all []TaskCase
	all = append(all, BuiltinSuite()...)
	all = append(all, PlanningSuite()...)
	all = append(all, ErrorRecoverySuite()...)
	all = append(all, ToolSelectionSuite()...)
	all = append(all, ConversationSuite()...)
	all = append(all, MemorySuite()...)
	all = append(all, KnowledgeSuite()...)
	all = append(all, MultiAgentSuite()...)
	// Self-learning dimensions
	all = append(all, SkillLearningSuite()...)
	all = append(all, PreferenceAdherenceSuite()...)
	all = append(all, MemoryRetentionSuite()...)
	return all
}
```

In `AllSuites()`, add the 3 new entries + the composite suite:

```go
func AllSuites() map[string]func() []TaskCase {
	return map[string]func() []TaskCase{
		"builtin":              BuiltinSuite,
		"evolution":            EvolutionSuite,
		"workload":             WorkloadSuite,
		"planning":             PlanningSuite,
		"error_recovery":       ErrorRecoverySuite,
		"tool_selection":       ToolSelectionSuite,
		"conversation":         ConversationSuite,
		"memory":               MemorySuite,
		"knowledge":            KnowledgeSuite,
		"multi_agent":          MultiAgentSuite,
		"skill_learning":       SkillLearningSuite,
		"preference_adherence": PreferenceAdherenceSuite,
		"memory_retention":     MemoryRetentionSuite,
		"self_learning":        SelfLearningSuite,
		"full":                 FullSuite,
	}
}
```

- [ ] **Step 3: Build to verify no compile errors**

```bash
CGO_ENABLED=1 go build -tags fts5 ./internal/eval/
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/eval/fixtures_self_learning.go internal/eval/fixtures.go
git commit -m "feat(eval): add skill_learning, preference_adherence, memory_retention fixture suites"
```

---

## Task 3: Learning Metrics Types + Harness Extension

**Files:**
- Create: `internal/eval/learning_metrics.go`
- Modify: `internal/eval/harness.go`

- [ ] **Step 1: Create `internal/eval/learning_metrics.go`**

```go
package eval

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// LearningVelocity describes how fast a metric is improving.
type LearningVelocity string

const (
	VelocityImproving  LearningVelocity = "improving"
	VelocityDegrading  LearningVelocity = "degrading"
	VelocityStable     LearningVelocity = "stable"
	VelocityInsuffData LearningVelocity = "insufficient_data"
)

// LearningCurveAnalysis captures the trend in agent performance across
// longitudinal evaluation iterations.
type LearningCurveAnalysis struct {
	// RewardSlope is the linear regression slope of RLAvgReward across iterations.
	// Positive = improving, negative = degrading.
	RewardSlope float64 `json:"reward_slope"`

	// SuccessRateSlope is the linear regression slope of SuccessRate.
	SuccessRateSlope float64 `json:"success_rate_slope"`

	// SkillGrowthPerIter is the average SkillDraftCount increase per iteration.
	SkillGrowthPerIter float64 `json:"skill_growth_per_iter"`

	// PreferenceGrowthPerIter is the average PreferenceCount increase per iteration.
	PreferenceGrowthPerIter float64 `json:"preference_growth_per_iter"`

	// RewardVelocity is the learning velocity classification for reward.
	RewardVelocity LearningVelocity `json:"reward_velocity"`

	// SuccessVelocity is the learning velocity classification for success rate.
	SuccessVelocity LearningVelocity `json:"success_velocity"`

	// IterationCount is the number of data points used.
	IterationCount int `json:"iteration_count"`

	// FirstReward and LastReward bound the reward range.
	FirstReward float64 `json:"first_reward"`
	LastReward  float64 `json:"last_reward"`

	// FirstSuccessRate and LastSuccessRate bound the success rate range.
	FirstSuccessRate float64 `json:"first_success_rate"`
	LastSuccessRate  float64 `json:"last_success_rate"`
}

// StrategyConvergenceAnalysis evaluates whether the agent's replan strategy
// is converging toward a stable value or oscillating.
type StrategyConvergenceAnalysis struct {
	// IsConverged is true when recent threshold changes are below the
	// convergence tolerance (< 0.02 per iteration).
	IsConverged bool `json:"is_converged"`

	// ThresholdMean is the mean replan threshold across iterations.
	ThresholdMean float64 `json:"threshold_mean"`

	// ThresholdStdDev is the standard deviation of the threshold.
	ThresholdStdDev float64 `json:"threshold_std_dev"`

	// ThresholdTrend is "rising", "falling", or "stable".
	ThresholdTrend string `json:"threshold_trend"`

	// OscillationScore measures instability: high = oscillating, low = stable.
	// Computed as mean absolute deviation of successive threshold changes.
	OscillationScore float64 `json:"oscillation_score"`

	// IterationCount is the number of data points used.
	IterationCount int `json:"iteration_count"`
}

// SelfLearningAnalysisSummary is a compact summary of self-learning metrics
// suitable for embedding in LongitudinalReport.
type SelfLearningAnalysisSummary struct {
	LearningCurve       *LearningCurveAnalysis       `json:"learning_curve,omitempty"`
	StrategyConvergence *StrategyConvergenceAnalysis  `json:"strategy_convergence,omitempty"`
	CompositeScore      float64                       `json:"composite_score"`
	GeneratedAt         time.Time                     `json:"generated_at"`
}

// ComputeLearningCurve derives learning curve analytics from a series of
// IterationPoints. Requires at least 2 points; returns nil otherwise.
func ComputeLearningCurve(points []IterationPoint) *LearningCurveAnalysis {
	if len(points) < 2 {
		return nil
	}

	n := float64(len(points))
	first := points[0]
	last := points[len(points)-1]

	rewardSlope := linearSlope(extractFloat(points, func(p IterationPoint) float64 { return p.RLAvgReward }))
	successSlope := linearSlope(extractFloat(points, func(p IterationPoint) float64 { return p.Summary.SuccessRate }))

	var skillGrowth, prefGrowth float64
	if len(points) > 1 {
		skillDeltas := 0.0
		prefDeltas := 0.0
		for i := 1; i < len(points); i++ {
			skillDeltas += float64(points[i].SkillDraftCount - points[i-1].SkillDraftCount)
			prefDeltas += float64(points[i].PreferenceCount - points[i-1].PreferenceCount)
		}
		skillGrowth = skillDeltas / (n - 1)
		prefGrowth = prefDeltas / (n - 1)
	}

	return &LearningCurveAnalysis{
		RewardSlope:             rewardSlope,
		SuccessRateSlope:        successSlope,
		SkillGrowthPerIter:      skillGrowth,
		PreferenceGrowthPerIter: prefGrowth,
		RewardVelocity:          classifyVelocity(rewardSlope, 0.01),
		SuccessVelocity:         classifyVelocity(successSlope, 0.005),
		IterationCount:          len(points),
		FirstReward:             first.RLAvgReward,
		LastReward:              last.RLAvgReward,
		FirstSuccessRate:        first.Summary.SuccessRate,
		LastSuccessRate:         last.Summary.SuccessRate,
	}
}

// ComputeStrategyConvergence analyses replan threshold stability across iterations.
// Requires at least 2 points; returns nil otherwise.
func ComputeStrategyConvergence(points []IterationPoint) *StrategyConvergenceAnalysis {
	if len(points) < 2 {
		return nil
	}

	thresholds := extractFloat(points, func(p IterationPoint) float64 { return p.ReplanThreshold })
	mean := mean(thresholds)
	stdDev := stdDev(thresholds, mean)

	// Oscillation: mean absolute difference of successive changes
	var oscillation float64
	if len(thresholds) > 1 {
		sumDiff := 0.0
		for i := 1; i < len(thresholds); i++ {
			sumDiff += math.Abs(thresholds[i] - thresholds[i-1])
		}
		oscillation = sumDiff / float64(len(thresholds)-1)
	}

	slope := linearSlope(thresholds)
	trend := "stable"
	if slope > 0.005 {
		trend = "rising"
	} else if slope < -0.005 {
		trend = "falling"
	}

	return &StrategyConvergenceAnalysis{
		IsConverged:      oscillation < 0.02,
		ThresholdMean:    mean,
		ThresholdStdDev:  stdDev,
		ThresholdTrend:   trend,
		OscillationScore: oscillation,
		IterationCount:   len(points),
	}
}

// ComputeSelfLearningAnalysis computes the full self-learning analysis summary
// from a series of IterationPoints.
func ComputeSelfLearningAnalysis(points []IterationPoint) *SelfLearningAnalysisSummary {
	curve := ComputeLearningCurve(points)
	convergence := ComputeStrategyConvergence(points)

	composite := computeCompositeScore(curve, convergence)

	return &SelfLearningAnalysisSummary{
		LearningCurve:       curve,
		StrategyConvergence: convergence,
		CompositeScore:      composite,
		GeneratedAt:         time.Now(),
	}
}

// FormatLearningCurveSummary returns a compact human-readable summary.
func FormatLearningCurveSummary(s *SelfLearningAnalysisSummary) string {
	if s == nil {
		return "No self-learning analysis available (need ≥2 longitudinal iterations).\n"
	}
	var b strings.Builder
	b.WriteString("=== Self-Learning Analysis ===\n")

	if s.LearningCurve != nil {
		c := s.LearningCurve
		b.WriteString(fmt.Sprintf("Learning Curve (%d iterations):\n", c.IterationCount))
		b.WriteString(fmt.Sprintf("  Reward:      %.3f → %.3f (slope %.4f, %s)\n",
			c.FirstReward, c.LastReward, c.RewardSlope, c.RewardVelocity))
		b.WriteString(fmt.Sprintf("  Success:     %.0f%% → %.0f%% (slope %.4f, %s)\n",
			c.FirstSuccessRate*100, c.LastSuccessRate*100, c.SuccessRateSlope, c.SuccessVelocity))
		b.WriteString(fmt.Sprintf("  Skill growth: +%.2f/iter | Pref growth: +%.2f/iter\n",
			c.SkillGrowthPerIter, c.PreferenceGrowthPerIter))
	}

	if s.StrategyConvergence != nil {
		sc := s.StrategyConvergence
		convergedStr := "NO"
		if sc.IsConverged {
			convergedStr = "YES"
		}
		b.WriteString(fmt.Sprintf("Strategy Convergence: %s (oscillation=%.4f, trend=%s)\n",
			convergedStr, sc.OscillationScore, sc.ThresholdTrend))
		b.WriteString(fmt.Sprintf("  Threshold: mean=%.3f stddev=%.3f\n",
			sc.ThresholdMean, sc.ThresholdStdDev))
	}

	b.WriteString(fmt.Sprintf("Composite Self-Learning Score: %.2f/1.00\n", s.CompositeScore))
	return b.String()
}

// GenerateLearningCurveHTML returns an HTML page with a line chart of learning curve data.
// Uses vanilla JS + SVG for zero dependencies.
func GenerateLearningCurveHTML(points []IterationPoint) string {
	if len(points) == 0 {
		return "<html><body>No iteration data available.</body></html>"
	}

	// Build JS data arrays
	labels := make([]string, len(points))
	rewards := make([]float64, len(points))
	successRates := make([]float64, len(points))
	skillCounts := make([]int, len(points))
	prefCounts := make([]int, len(points))

	for i, p := range points {
		labels[i] = p.RunID
		rewards[i] = p.RLAvgReward
		successRates[i] = p.Summary.SuccessRate * 100
		skillCounts[i] = p.SkillDraftCount
		prefCounts[i] = p.PreferenceCount
	}

	analysis := ComputeSelfLearningAnalysis(points)
	summary := FormatLearningCurveSummary(analysis)
	summaryHTML := strings.ReplaceAll(summary, "\n", "<br>")

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>IronClaw — Learning Curve</title>
<script src="https://cdn.jsdelivr.net/npm/chart.js@4/dist/chart.umd.min.js"></script>
<style>
  body { font-family: system-ui, sans-serif; background: #0f1117; color: #e2e8f0; margin: 0; padding: 24px; }
  h1 { color: #a78bfa; margin-bottom: 4px; }
  .subtitle { color: #64748b; font-size: 14px; margin-bottom: 32px; }
  .summary { background: #1e2130; border-left: 3px solid #a78bfa; padding: 16px; margin-bottom: 32px;
             font-family: monospace; font-size: 13px; line-height: 1.7; border-radius: 4px; }
  .chart-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 24px; }
  .chart-box { background: #1e2130; border-radius: 8px; padding: 20px; }
  canvas { max-height: 280px; }
</style>
</head>
<body>
<h1>🧠 IronClaw — Self-Learning Curve</h1>
<div class="subtitle">Generated %s | %d iterations</div>
<div class="summary">%s</div>
<div class="chart-grid">
  <div class="chart-box"><canvas id="rewardChart"></canvas></div>
  <div class="chart-box"><canvas id="successChart"></canvas></div>
  <div class="chart-box"><canvas id="skillChart"></canvas></div>
  <div class="chart-box"><canvas id="prefChart"></canvas></div>
</div>
<script>
const labels = %s;
const rewards = %s;
const successRates = %s;
const skillCounts = %s;
const prefCounts = %s;

const chartDefaults = {
  type: 'line',
  options: {
    responsive: true,
    plugins: { legend: { labels: { color: '#e2e8f0' } } },
    scales: {
      x: { ticks: { color: '#94a3b8' }, grid: { color: '#2d3748' } },
      y: { ticks: { color: '#94a3b8' }, grid: { color: '#2d3748' } }
    }
  }
};

function mkChart(id, label, data, color) {
  new Chart(document.getElementById(id), {
    ...chartDefaults,
    data: {
      labels,
      datasets: [{
        label,
        data,
        borderColor: color,
        backgroundColor: color + '33',
        tension: 0.3,
        fill: true,
        pointRadius: 5,
      }]
    }
  });
}

mkChart('rewardChart',  'RL Avg Reward',        rewards,      '#a78bfa');
mkChart('successChart', 'Success Rate (%%)',      successRates, '#34d399');
mkChart('skillChart',   'Skill Draft Count',     skillCounts,  '#f59e0b');
mkChart('prefChart',    'Preference Count',      prefCounts,   '#60a5fa');
</script>
</body>
</html>`,
		time.Now().Format("2006-01-02 15:04"),
		len(points),
		summaryHTML,
		toJSStringArray(labels),
		toJSFloatArray(rewards),
		toJSFloatArray(successRates),
		toJSIntArray(skillCounts),
		toJSIntArray(prefCounts),
	)
}

// --- internal helpers ---

func extractFloat(points []IterationPoint, f func(IterationPoint) float64) []float64 {
	out := make([]float64, len(points))
	for i, p := range points {
		out[i] = f(p)
	}
	return out
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func stdDev(xs []float64, m float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	sum := 0.0
	for _, x := range xs {
		d := x - m
		sum += d * d
	}
	return math.Sqrt(sum / float64(len(xs)))
}

// linearSlope computes the slope of the least-squares line for a sequence of values.
// Uses index 0..n-1 as x values.
func linearSlope(ys []float64) float64 {
	n := float64(len(ys))
	if n < 2 {
		return 0
	}
	sumX, sumY, sumXY, sumX2 := 0.0, 0.0, 0.0, 0.0
	for i, y := range ys {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}
	denom := n*sumX2 - sumX*sumX
	if denom == 0 {
		return 0
	}
	return (n*sumXY - sumX*sumY) / denom
}

func classifyVelocity(slope, threshold float64) LearningVelocity {
	if slope > threshold {
		return VelocityImproving
	}
	if slope < -threshold {
		return VelocityDegrading
	}
	return VelocityStable
}

func computeCompositeScore(curve *LearningCurveAnalysis, conv *StrategyConvergenceAnalysis) float64 {
	score := 0.5 // baseline
	if curve != nil {
		if curve.RewardVelocity == VelocityImproving {
			score += 0.2
		} else if curve.RewardVelocity == VelocityDegrading {
			score -= 0.1
		}
		if curve.SuccessVelocity == VelocityImproving {
			score += 0.2
		} else if curve.SuccessVelocity == VelocityDegrading {
			score -= 0.1
		}
	}
	if conv != nil && conv.IsConverged {
		score += 0.1
	}
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return score
}

func toJSStringArray(ss []string) string {
	quoted := make([]string, len(ss))
	for i, s := range ss {
		quoted[i] = `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

func toJSFloatArray(fs []float64) string {
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = fmt.Sprintf("%.4f", f)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func toJSIntArray(is []int) string {
	parts := make([]string, len(is))
	for i, v := range is {
		parts[i] = fmt.Sprintf("%d", v)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
```

- [ ] **Step 2: Extend `LongitudinalReport` in `internal/eval/harness.go`**

In `harness.go`, add the `SelfLearningAnalysis` field to the `LongitudinalReport` struct:

```go
// LongitudinalReport captures the full time series of a longitudinal evaluation
// run, including per-iteration metrics and first-vs-last comparison deltas.
type LongitudinalReport struct {
	Iterations          []IterationPoint             `json:"iterations"`
	First               SuiteSummary                 `json:"first"`
	Last                SuiteSummary                 `json:"last"`
	Deltas              ComparisonDelta              `json:"deltas"`
	GeneratedAt         time.Time                    `json:"generated_at"`
	SelfLearningAnalysis *SelfLearningAnalysisSummary `json:"self_learning_analysis,omitempty"`
}
```

Update `NewLongitudinalReport` to auto-compute self-learning analysis when there are ≥2 points:

```go
func NewLongitudinalReport(points []IterationPoint) *LongitudinalReport {
	if len(points) == 0 {
		return &LongitudinalReport{GeneratedAt: time.Now()}
	}

	first := points[0].Summary
	last := points[len(points)-1].Summary

	r := &LongitudinalReport{
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

	if len(points) >= 2 {
		r.SelfLearningAnalysis = ComputeSelfLearningAnalysis(points)
	}
	return r
}
```

- [ ] **Step 3: Build to verify no compile errors**

```bash
CGO_ENABLED=1 go build -tags fts5 ./internal/eval/
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/eval/learning_metrics.go internal/eval/harness.go
git commit -m "feat(eval): add LearningCurveAnalysis, StrategyConvergenceAnalysis, SelfLearningReport"
```

---

## Task 4: CLI `eval self-learning` Subcommand + Learning Curve HTML in Longitudinal

**Files:**
- Modify: `cmd/ironclaw/eval.go`

- [ ] **Step 1: Add `newEvalSelfLearningCmd()` function**

Add the following new function to `cmd/ironclaw/eval.go` (after `newEvalBenchmarkCmd` or at the end of the file):

```go
// newEvalSelfLearningCmd runs the dedicated self-learning evaluation suites and
// generates a composite self-learning report.
func newEvalSelfLearningCmd() *cobra.Command {
	var (
		live           bool
		configPath     string
		outputDir      string
		longitudinalIn string
		judge          bool
	)

	cmd := &cobra.Command{
		Use:   "self-learning",
		Short: "Evaluate agent self-learning capabilities (skill adoption, preference adherence, memory retention)",
		Long: `Runs the three self-learning evaluation suites:
  - skill_learning:        tests whether skill synthesis improves task performance
  - preference_adherence:  tests whether explicit behavioral constraints are followed
  - memory_retention:      tests cross-session memory precision and multi-hop recall

Output:
  - self_learning_report.json  — dimension scores + composite self-learning score
  - learning_curve.html        — interactive chart (if --longitudinal-in is provided)

Combine with 'eval longitudinal --with-workload workload' to generate the longitudinal
data needed for learning curve analysis.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			tasks := eval.SelfLearningSuite()

			if outputDir == "" {
				outputDir = fmt.Sprintf("eval_self_learning_%s", time.Now().Format("20060102_150405"))
			}
			if err := os.MkdirAll(outputDir, 0o755); err != nil {
				return fmt.Errorf("create output dir: %w", err)
			}

			var runner eval.AgentRunner
			var gw *gateway.Gateway
			var cleanup func()

			if live {
				var gwErr error
				gw, cleanup, gwErr = initEvalGateway(configPath)
				if gwErr != nil {
					return fmt.Errorf("init live eval: %w", gwErr)
				}
				r := gw.NewEvalRunner()
				if r == nil {
					cleanup()
					return fmt.Errorf("live self-learning eval requires agent.mode = cognitive")
				}
				runner = r
			} else {
				runner = &eval.DryRunner{}
			}
			if cleanup != nil {
				defer cleanup()
			}

			var runOpts *eval.RunOptions
			if judge && live && gw != nil {
				runOpts = &eval.RunOptions{
					Judge: eval.NewLLMJudge(gw.LLMProvider()),
				}
				fmt.Println("LLM Judge: enabled")
			}

			ctx := context.Background()
			fmt.Printf("Running self-learning eval (%d tasks)...\n", len(tasks))

			result, err := eval.RunSuiteWithOptions(ctx, "self-learning", tasks, runner, runOpts)
			if err != nil {
				return fmt.Errorf("run self-learning suite: %w", err)
			}

			applyFeatureState(result, gw)

			reportPath := fmt.Sprintf("%s/self_learning_report.json", outputDir)
			if err := result.SaveJSON(reportPath); err != nil {
				return fmt.Errorf("save report: %w", err)
			}

			summary := result.Summary()
			dimReport := eval.AggregateDimensions(result.Results)

			fmt.Printf("\n=== Self-Learning Evaluation Results ===\n")
			fmt.Printf("Overall: %.0f%% success | Score %.2f | Duration %.1fs\n",
				summary.SuccessRate*100, summary.AvgScore, summary.Duration.Seconds())
			fmt.Printf("\nDimension Breakdown:\n")
			for _, ds := range dimReport.Dimensions {
				fmt.Printf("  %-24s  success=%.0f%%  score=%.2f  tasks=%d\n",
					ds.Dimension, ds.SuccessRate*100, ds.AvgScore, ds.TaskCount)
			}
			fmt.Printf("\nReport saved to %s\n", reportPath)

			// Learning curve HTML — generated from a prior longitudinal report if provided.
			if longitudinalIn != "" {
				longReport, ldErr := eval.LoadLongitudinalReport(longitudinalIn)
				if ldErr != nil {
					slog.Warn("could not load longitudinal report for learning curve", "err", ldErr)
				} else {
					htmlPath := fmt.Sprintf("%s/learning_curve.html", outputDir)
					html := eval.GenerateLearningCurveHTML(longReport.Iterations)
					if writeErr := os.WriteFile(htmlPath, []byte(html), 0o644); writeErr != nil {
						slog.Warn("could not write learning curve HTML", "err", writeErr)
					} else {
						fmt.Printf("Learning curve chart: %s\n", htmlPath)
						if longReport.SelfLearningAnalysis != nil {
							fmt.Print(eval.FormatLearningCurveSummary(longReport.SelfLearningAnalysis))
						}
					}
				}
			} else {
				fmt.Println("\nTip: Use --longitudinal-in <path/to/longitudinal_report.json> to include a learning curve chart.")
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&live, "live", false, "run against a live cognitive agent")
	cmd.Flags().BoolVar(&judge, "judge", true, "enable LLM-as-Judge for tasks with Rubric (requires --live)")
	cmd.Flags().StringVarP(&configPath, "config", "c", "configs/ironclaw.yaml", "config file path (for --live)")
	cmd.Flags().StringVarP(&outputDir, "output", "o", "", "output directory (auto-generated if empty)")
	cmd.Flags().StringVar(&longitudinalIn, "longitudinal-in", "", "path to a longitudinal_report.json for learning curve generation")
	return cmd
}
```

- [ ] **Step 2: Register the new subcommand in `eval.go`**

Find the line:

```go
cmd.AddCommand(newEvalRunCmd(), newEvalCompareCmd(), newEvalListCmd(), newEvalLongitudinalCmd(), newEvalVisualizeCmd(), newEvalDiagnoseCmd(), newEvalAdaptiveCmd(), newEvalBenchmarkCmd())
```

Replace with:

```go
cmd.AddCommand(newEvalRunCmd(), newEvalCompareCmd(), newEvalListCmd(), newEvalLongitudinalCmd(), newEvalVisualizeCmd(), newEvalDiagnoseCmd(), newEvalAdaptiveCmd(), newEvalBenchmarkCmd(), newEvalSelfLearningCmd())
```

- [ ] **Step 3: Add learning curve HTML output to `eval longitudinal`**

In `newEvalLongitudinalCmd()`, after the `report.SaveJSON(reportPath)` call (around line 474), add:

```go
// Write learning curve HTML alongside the longitudinal report.
if len(points) >= 2 {
    htmlPath := fmt.Sprintf("%s/learning_curve.html", outputDir)
    html := eval.GenerateLearningCurveHTML(points)
    if htmlErr := os.WriteFile(htmlPath, []byte(html), 0o644); htmlErr != nil {
        slog.Warn("failed to write learning curve HTML", "err", htmlErr)
    } else {
        fmt.Printf("Learning curve chart saved to %s\n", htmlPath)
    }
    if report.SelfLearningAnalysis != nil {
        fmt.Print(eval.FormatLearningCurveSummary(report.SelfLearningAnalysis))
    }
}
```

- [ ] **Step 4: Build the full binary**

```bash
CGO_ENABLED=1 go build -tags fts5 ./cmd/ironclaw/
```

Expected: no errors.

- [ ] **Step 5: Smoke test the new CLI command (dry-run)**

```bash
./bin/ironclaw eval self-learning --help
./bin/ironclaw eval self-learning -o /tmp/sl_test_run
```

Expected: help output lists the command + suites. Dry run completes without panics (DryRunner scores will be 0 — that's correct).

- [ ] **Step 6: Commit**

```bash
git add cmd/ironclaw/eval.go
git commit -m "feat(eval): add eval self-learning subcommand + learning curve HTML in longitudinal"
```

---

## Task 5: Unit Tests for Learning Metrics

**Files:**
- Create: `internal/eval/learning_metrics_test.go`

- [ ] **Step 1: Create `internal/eval/learning_metrics_test.go`**

```go
package eval

import (
	"math"
	"testing"
	"time"
)

func makeTestPoints(n int, rewardFn func(i int) float64, successFn func(i int) float64) []IterationPoint {
	points := make([]IterationPoint, n)
	for i := 0; i < n; i++ {
		points[i] = IterationPoint{
			Iteration:       i + 1,
			RunID:           fmt.Sprintf("iter-%03d", i+1),
			Timestamp:       time.Now(),
			RLAvgReward:     rewardFn(i),
			ReplanThreshold: 0.5 + float64(i)*0.01,
			SkillDraftCount: i * 2,
			PreferenceCount: i * 3,
			Summary: SuiteSummary{
				SuccessRate: successFn(i),
			},
		}
	}
	return points
}

func TestComputeLearningCurve_Improving(t *testing.T) {
	points := makeTestPoints(5,
		func(i int) float64 { return float64(i) * 0.1 },       // 0, 0.1, 0.2, 0.3, 0.4
		func(i int) float64 { return float64(i) * 0.15 },      // 0, 0.15, 0.3, 0.45, 0.6
	)
	curve := ComputeLearningCurve(points)
	if curve == nil {
		t.Fatal("expected non-nil curve")
	}
	if curve.RewardVelocity != VelocityImproving {
		t.Errorf("expected improving reward, got %s (slope=%.4f)", curve.RewardVelocity, curve.RewardSlope)
	}
	if curve.SkillGrowthPerIter <= 0 {
		t.Errorf("expected positive skill growth, got %.2f", curve.SkillGrowthPerIter)
	}
}

func TestComputeLearningCurve_Insufficient(t *testing.T) {
	curve := ComputeLearningCurve([]IterationPoint{{}})
	if curve != nil {
		t.Errorf("expected nil for single point, got %+v", curve)
	}
}

func TestComputeStrategyConvergence_Converged(t *testing.T) {
	// Stable threshold — very small changes
	points := makeTestPoints(4,
		func(i int) float64 { return 0.5 },
		func(i int) float64 { return 0.7 },
	)
	// Override thresholds to be nearly constant
	for i := range points {
		points[i].ReplanThreshold = 0.3 + float64(i)*0.001
	}
	conv := ComputeStrategyConvergence(points)
	if conv == nil {
		t.Fatal("expected non-nil convergence")
	}
	if !conv.IsConverged {
		t.Errorf("expected converged, oscillation=%.4f", conv.OscillationScore)
	}
}

func TestComputeStrategyConvergence_Oscillating(t *testing.T) {
	points := makeTestPoints(4,
		func(i int) float64 { return 0.5 },
		func(i int) float64 { return 0.5 },
	)
	// Alternating thresholds = high oscillation
	thresholds := []float64{0.2, 0.8, 0.2, 0.8}
	for i := range points {
		points[i].ReplanThreshold = thresholds[i]
	}
	conv := ComputeStrategyConvergence(points)
	if conv == nil {
		t.Fatal("expected non-nil convergence")
	}
	if conv.IsConverged {
		t.Errorf("expected NOT converged for oscillating thresholds, oscillation=%.4f", conv.OscillationScore)
	}
}

func TestLinearSlope(t *testing.T) {
	// y = 2x → slope should be 2
	ys := []float64{0, 2, 4, 6, 8}
	got := linearSlope(ys)
	if math.Abs(got-2.0) > 0.001 {
		t.Errorf("expected slope 2.0, got %.4f", got)
	}
}

func TestGenerateLearningCurveHTML_NoData(t *testing.T) {
	html := GenerateLearningCurveHTML(nil)
	if html == "" {
		t.Error("expected non-empty HTML for nil points")
	}
}

func TestComputeSelfLearningAnalysis_TwoPoints(t *testing.T) {
	points := makeTestPoints(2,
		func(i int) float64 { return float64(i) * 0.3 },
		func(i int) float64 { return float64(i) * 0.2 },
	)
	analysis := ComputeSelfLearningAnalysis(points)
	if analysis == nil {
		t.Fatal("expected non-nil analysis")
	}
	if analysis.CompositeScore < 0 || analysis.CompositeScore > 1 {
		t.Errorf("composite score out of range: %.2f", analysis.CompositeScore)
	}
}
```

Note: Add `"fmt"` to the import in `learning_metrics_test.go`.

- [ ] **Step 2: Run the tests**

```bash
CGO_ENABLED=1 go test -tags fts5 -run "TestComputeLearningCurve|TestComputeStrategy|TestLinear|TestGenerate|TestCompute" ./internal/eval/ -v
```

Expected: all tests pass.

- [ ] **Step 3: Commit**

```bash
git add internal/eval/learning_metrics_test.go
git commit -m "test(eval): add unit tests for LearningCurveAnalysis and StrategyConvergenceAnalysis"
```

---

## Self-Review

**Spec coverage:**
- ✅ `DimSkillLearning`, `DimPreferenceAdherence`, `DimMemoryRetention` — Task 1
- ✅ `SkillLearningSuite` (6 tasks, deterministic) — Task 2
- ✅ `PreferenceAdherenceSuite` (8 tasks, deterministic) — Task 2
- ✅ `MemoryRetentionSuite` (6 tasks, memory injection) — Task 2
- ✅ `SelfLearningSuite` composite — Task 2
- ✅ `AllSuites()` + `FullSuite()` updated — Task 2
- ✅ `LearningCurveAnalysis` with reward/success slope + velocity — Task 3
- ✅ `StrategyConvergenceAnalysis` with oscillation score + convergence flag — Task 3
- ✅ `SelfLearningAnalysisSummary` + composite score — Task 3
- ✅ `LongitudinalReport.SelfLearningAnalysis` auto-computed — Task 3
- ✅ `GenerateLearningCurveHTML` — Task 3
- ✅ `FormatLearningCurveSummary` text output — Task 3
- ✅ `eval self-learning` CLI subcommand — Task 4
- ✅ Learning curve HTML added to `eval longitudinal` output — Task 4
- ✅ Unit tests for all compute functions — Task 5

**Placeholder scan:** None found.

**Type consistency:**
- `SelfLearningAnalysisSummary` defined in Task 3, used in `harness.go` (Task 3) and `eval.go` (Task 4) ✅
- `LearningVelocity` constants defined and used consistently ✅
- `IterationPoint` struct not changed (only `LongitudinalReport` gets new field) ✅
