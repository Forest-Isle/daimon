package eval

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// LongitudinalConfig configures a longitudinal evaluation run.
type LongitudinalConfig struct {
	// Rounds is the number of eval→insights→eval cycles to run. Default: 2.
	Rounds int
	// MinTrajectories is the minimum trajectory count before RunInsightsCycle
	// is triggered. Matches the engine's internal threshold of 5.
	MinTrajectories int
	// OutputDir is where per-round JSON results are written (optional).
	OutputDir string
}

// LongitudinalResult holds the results across all rounds.
type LongitudinalResult struct {
	Config      LongitudinalConfig  `json:"config"`
	Rounds      []LongitudinalRound `json:"rounds"`
	StartedAt   time.Time           `json:"started_at"`
	CompletedAt time.Time           `json:"completed_at"`
}

// LongitudinalRound holds results for one eval→insights cycle.
type LongitudinalRound struct {
	RoundNumber    int               `json:"round"`
	Suite          *SuiteResult      `json:"suite"`
	InsightsRan    bool              `json:"insights_ran"`
	InsightsReason string            `json:"insights_reason,omitempty"`
	Comparison     *ComparisonReport `json:"comparison,omitempty"` // nil for round 0
	Duration       time.Duration     `json:"duration_ms"`
}

// InsightsTrigger is implemented by runners that can trigger the evolution
// engine's insights cycle directly (without waiting for the 6-hour timer).
type InsightsTrigger interface {
	RunInsightsCycle() (ran bool, reason string)
	TrajectoryCount() int
}

// RunLongitudinal executes a multi-round eval with insights cycles between rounds.
// The runner must implement InsightsTrigger for insights to fire between rounds.
func RunLongitudinal(ctx context.Context, runner AgentRunner, tasks []TaskCase, cfg LongitudinalConfig) (*LongitudinalResult, error) {
	if cfg.Rounds <= 0 {
		cfg.Rounds = 2
	}
	if cfg.MinTrajectories <= 0 {
		cfg.MinTrajectories = 5
	}

	result := &LongitudinalResult{
		Config:    cfg,
		StartedAt: time.Now(),
	}

	var prevSuite *SuiteResult

	for round := 0; round < cfg.Rounds; round++ {
		roundStart := time.Now()

		runID := fmt.Sprintf("longitudinal-r%d-%d", round, time.Now().UnixNano())
		suite, err := RunSuite(ctx, runID, tasks, runner)
		if err != nil {
			return result, fmt.Errorf("round %d: %w", round, err)
		}

		r := LongitudinalRound{
			RoundNumber: round,
			Suite:       suite,
			Duration:    time.Since(roundStart),
		}

		// Compare against previous round if available.
		if prevSuite != nil {
			r.Comparison = Compare(prevSuite, suite)
		}

		// Trigger insights cycle between rounds (not after last round).
		if round < cfg.Rounds-1 {
			if it, ok := runner.(InsightsTrigger); ok {
				count := it.TrajectoryCount()
				if count >= cfg.MinTrajectories {
					ran, reason := it.RunInsightsCycle()
					r.InsightsRan = ran
					r.InsightsReason = reason
				} else {
					r.InsightsReason = fmt.Sprintf("skipped: only %d trajectories (need %d)", count, cfg.MinTrajectories)
				}
			} else {
				r.InsightsReason = "runner does not implement InsightsTrigger"
			}
		}

		// Optionally write per-round JSON to OutputDir.
		if cfg.OutputDir != "" && suite != nil {
			path := fmt.Sprintf("%s/round_%d.json", cfg.OutputDir, round)
			_ = suite.SaveJSON(path)
		}

		result.Rounds = append(result.Rounds, r)
		prevSuite = suite
	}

	result.CompletedAt = time.Now()
	return result, nil
}

// FormatMarkdown renders the longitudinal result as a human-readable Markdown report.
func (r *LongitudinalResult) FormatMarkdown() string {
	var b strings.Builder
	b.WriteString("# Longitudinal Evaluation Report\n\n")
	fmt.Fprintf(&b, "Rounds: %d | Started: %s | Duration: %s\n\n",
		r.Config.Rounds,
		r.StartedAt.Format(time.RFC3339),
		r.CompletedAt.Sub(r.StartedAt).Round(time.Second),
	)

	for _, round := range r.Rounds {
		fmt.Fprintf(&b, "## Round %d\n\n", round.RoundNumber)
		if round.Suite != nil {
			sum := round.Suite.Summary()
			fmt.Fprintf(&b, "- Success Rate: %.1f%%\n", sum.SuccessRate*100)
			fmt.Fprintf(&b, "- Assertion Pass Rate: %.1f%%\n", sum.AvgAssertionPassRate*100)
			fmt.Fprintf(&b, "- Tasks: %d\n", sum.TotalTasks)
		}
		if round.InsightsRan {
			b.WriteString("- **Insights cycle ran** ✓\n")
		} else if round.InsightsReason != "" {
			fmt.Fprintf(&b, "- Insights: %s\n", round.InsightsReason)
		}
		if round.Comparison != nil {
			fmt.Fprintf(&b, "- vs previous round: success rate delta = %+.1f%%\n",
				round.Comparison.Deltas.SuccessRateDelta*100)
		}
		b.WriteString("\n")
	}
	return b.String()
}
