package eval

import (
	"fmt"
	"strings"
	"time"
)

// TaskRegression tracks per-task score changes between two evaluation runs.
type TaskRegression struct {
	TaskID      string    `json:"task_id"`
	Dimension   Dimension `json:"dimension,omitempty"`
	BeforeScore float64   `json:"before_score"`
	AfterScore  float64   `json:"after_score"`
	Delta       float64   `json:"delta"`
	Status      string    `json:"status"` // "improved", "regressed", "stable"
}

// EvoSnapshotDiff holds the per-field deltas between two EvolutionSnapshots.
type EvoSnapshotDiff struct {
	PreferenceCountDelta int `json:"preference_count_delta"`
	StrategyVersionDelta int `json:"strategy_version_delta"`
	SkillDraftCountDelta int `json:"skill_draft_count_delta"`
	TrajectoryCountDelta int `json:"trajectory_count_delta"`

	// RouterModelChanges lists models whose task-routing counts changed between
	// runs. Key is model name; value is delta (positive = more tasks routed there).
	// Only populated when both snapshots contain RouterDecisions data.
	RouterModelChanges map[string]int `json:"router_model_changes,omitempty"`
}

// ComparisonReport compares two evaluation runs side by side.
type ComparisonReport struct {
	BeforeRunID     string                `json:"before_run_id"`
	AfterRunID      string                `json:"after_run_id"`
	Before          SuiteSummary          `json:"before"`
	After           SuiteSummary          `json:"after"`
	Deltas          ComparisonDelta       `json:"deltas"`
	TaskRegressions []TaskRegression      `json:"task_regressions,omitempty"`
	Regressions     []TaskRegression      `json:"regressions,omitempty"`
	Improvements    []TaskRegression      `json:"improvements,omitempty"`
	DimensionDeltas map[Dimension]float64 `json:"dimension_deltas,omitempty"`
	EvoSnapshot     *EvoSnapshotDiff      `json:"evo_snapshot,omitempty"`
	// FeatureStateDiff lists features whose enablement changed between runs.
	// Non-empty means comparison scores may be affected by config differences.
	// Values are formatted as "enabled->disabled" or "disabled->enabled".
	// Only populated when both runs recorded FeatureState (live eval with --live).
	FeatureStateDiff map[string]string `json:"feature_state_diff,omitempty"`
	GeneratedAt      time.Time         `json:"generated_at"`
}

// ComparisonDelta holds the differences between two runs.
type ComparisonDelta struct {
	SuccessRateDelta       float64       `json:"success_rate_delta"`
	AvgAssertPassRateDelta float64       `json:"avg_assert_pass_rate_delta"`
	AvgConfidenceDelta     float64       `json:"avg_confidence_delta"`
	AvgReplanCountDelta    float64       `json:"avg_replan_count_delta"`
	DurationDelta          time.Duration `json:"duration_delta_ms"`
}

// Compare produces a side-by-side comparison of two suite results.
func Compare(before, after *SuiteResult) *ComparisonReport {
	bs := before.Summary()
	as := after.Summary()

	report := &ComparisonReport{
		BeforeRunID: before.RunID,
		AfterRunID:  after.RunID,
		Before:      bs,
		After:       as,
		Deltas: ComparisonDelta{
			SuccessRateDelta:       as.SuccessRate - bs.SuccessRate,
			AvgAssertPassRateDelta: as.AvgAssertionPassRate - bs.AvgAssertionPassRate,
			AvgConfidenceDelta:     as.AvgConfidence - bs.AvgConfidence,
			AvgReplanCountDelta:    as.AvgReplanCount - bs.AvgReplanCount,
			DurationDelta:          as.Duration - bs.Duration,
		},
		DimensionDeltas: make(map[Dimension]float64),
		GeneratedAt:     time.Now(),
	}

	beforeMap := make(map[string]EvalResult)
	for _, r := range before.Results {
		beforeMap[r.TaskID] = r
	}

	for _, ar := range after.Results {
		if br, ok := beforeMap[ar.TaskID]; ok {
			delta := ar.FinalScore - br.FinalScore
			status := "stable"
			if delta > 0.05 {
				status = "improved"
			} else if delta < -0.05 {
				status = "regressed"
			}

			tr := TaskRegression{
				TaskID:      ar.TaskID,
				Dimension:   ar.Dimension,
				BeforeScore: br.FinalScore,
				AfterScore:  ar.FinalScore,
				Delta:       delta,
				Status:      status,
			}
			report.TaskRegressions = append(report.TaskRegressions, tr)

			switch status {
			case "regressed":
				report.Regressions = append(report.Regressions, tr)
			case "improved":
				report.Improvements = append(report.Improvements, tr)
			}
		}
	}

	beforeDims := aggregateDimScores(before.Results)
	afterDims := aggregateDimScores(after.Results)
	for dim, afterScore := range afterDims {
		if beforeScore, ok := beforeDims[dim]; ok {
			report.DimensionDeltas[dim] = afterScore - beforeScore
		}
	}

	if before.EvoAfter != nil && after.EvoAfter != nil {
		diff := &EvoSnapshotDiff{
			PreferenceCountDelta: after.EvoAfter.PreferenceCount - before.EvoAfter.PreferenceCount,
			StrategyVersionDelta: after.EvoAfter.StrategyVersion - before.EvoAfter.StrategyVersion,
			SkillDraftCountDelta: after.EvoAfter.SkillDraftCount - before.EvoAfter.SkillDraftCount,
			TrajectoryCountDelta: after.EvoAfter.TrajectoryCount - before.EvoAfter.TrajectoryCount,
		}
		if len(before.EvoAfter.RouterDecisions) > 0 || len(after.EvoAfter.RouterDecisions) > 0 {
			changes := make(map[string]int)
			allModels := make(map[string]bool)
			for m := range before.EvoAfter.RouterDecisions {
				allModels[m] = true
			}
			for m := range after.EvoAfter.RouterDecisions {
				allModels[m] = true
			}
			for model := range allModels {
				delta := after.EvoAfter.RouterDecisions[model] - before.EvoAfter.RouterDecisions[model]
				if delta != 0 {
					changes[model] = delta
				}
			}
			if len(changes) > 0 {
				diff.RouterModelChanges = changes
			}
		}
		report.EvoSnapshot = diff
	}

	// Compute feature state diff — only meaningful when both runs recorded feature state.
	// If only one side has it, skip diff to avoid false positives (missing data ≠ disabled).
	if before.FeatureState != nil && after.FeatureState != nil {
		diff := make(map[string]string)
		allFeatures := make(map[string]bool)
		for k := range before.FeatureState {
			allFeatures[k] = true
		}
		for k := range after.FeatureState {
			allFeatures[k] = true
		}
		for name := range allFeatures {
			beforeVal, afterVal := before.FeatureState[name], after.FeatureState[name]
			if beforeVal != afterVal {
				// Format: "enabled->disabled" or "disabled->enabled"
				bStr, aStr := "disabled", "disabled"
				if beforeVal {
					bStr = "enabled"
				}
				if afterVal {
					aStr = "enabled"
				}
				diff[name] = fmt.Sprintf("%s->%s", bStr, aStr)
			}
		}
		if len(diff) > 0 {
			report.FeatureStateDiff = diff
		}
	}

	return report
}

func aggregateDimScores(results []EvalResult) map[Dimension]float64 {
	sums := make(map[Dimension]float64)
	counts := make(map[Dimension]int)
	for _, r := range results {
		dim := DefaultDimension(r.Dimension)
		sums[dim] += r.FinalScore
		counts[dim]++
	}
	avgs := make(map[Dimension]float64)
	for dim, sum := range sums {
		if counts[dim] > 0 {
			avgs[dim] = sum / float64(counts[dim])
		}
	}
	return avgs
}

// FormatMarkdown renders the comparison as a human-readable Markdown report.
func (r *ComparisonReport) FormatMarkdown() string {
	var b strings.Builder

	b.WriteString("# Evaluation Comparison Report\n\n")
	fmt.Fprintf(&b, "**Before**: %s | **After**: %s\n\n", r.BeforeRunID, r.AfterRunID)

	b.WriteString("| Metric | Before | After | Delta |\n")
	b.WriteString("|--------|--------|-------|-------|\n")

	fmt.Fprintf(&b, "| Success Rate | %.1f%% | %.1f%% | %s |\n",
		r.Before.SuccessRate*100, r.After.SuccessRate*100,
		fmtDelta(r.Deltas.SuccessRateDelta*100, "%%", true))

	fmt.Fprintf(&b, "| Assertion Pass Rate | %.1f%% | %.1f%% | %s |\n",
		r.Before.AvgAssertionPassRate*100, r.After.AvgAssertionPassRate*100,
		fmtDelta(r.Deltas.AvgAssertPassRateDelta*100, "%%", true))

	fmt.Fprintf(&b, "| Avg Confidence | %.2f | %.2f | %s |\n",
		r.Before.AvgConfidence, r.After.AvgConfidence,
		fmtDelta(r.Deltas.AvgConfidenceDelta, "", true))

	fmt.Fprintf(&b, "| Avg Replan Count | %.1f | %.1f | %s |\n",
		r.Before.AvgReplanCount, r.After.AvgReplanCount,
		fmtDelta(r.Deltas.AvgReplanCountDelta, "", false))

	fmt.Fprintf(&b, "| Total Duration | %.1fs | %.1fs | %s |\n",
		r.Before.Duration.Seconds(), r.After.Duration.Seconds(),
		fmtDelta(r.Deltas.DurationDelta.Seconds(), "s", false))

	b.WriteString("\n")

	if r.Deltas.SuccessRateDelta > 0 {
		b.WriteString("**Overall**: Improvement detected after evolution cycle.\n")
	} else if r.Deltas.SuccessRateDelta < 0 {
		b.WriteString("**Overall**: Regression detected — review strategy changes.\n")
	} else {
		b.WriteString("**Overall**: No change in success rate.\n")
	}

	if len(r.Regressions) > 0 {
		b.WriteString("\n### Regressions\n\n")
		b.WriteString("| Task | Dimension | Before | After | Delta |\n")
		b.WriteString("|------|-----------|--------|-------|-------|\n")
		for _, tr := range r.Regressions {
			fmt.Fprintf(&b, "| %s | %s | %.2f | %.2f | %.2f |\n",
				tr.TaskID, tr.Dimension, tr.BeforeScore, tr.AfterScore, tr.Delta)
		}
	}

	if len(r.Improvements) > 0 {
		b.WriteString("\n### Improvements\n\n")
		b.WriteString("| Task | Dimension | Before | After | Delta |\n")
		b.WriteString("|------|-----------|--------|-------|-------|\n")
		for _, tr := range r.Improvements {
			fmt.Fprintf(&b, "| %s | %s | %.2f | %.2f | +%.2f |\n",
				tr.TaskID, tr.Dimension, tr.BeforeScore, tr.AfterScore, tr.Delta)
		}
	}

	if len(r.DimensionDeltas) > 0 {
		b.WriteString("\n### Dimension Changes\n\n")
		b.WriteString("| Dimension | Delta |\n")
		b.WriteString("|-----------|-------|\n")
		for dim, delta := range r.DimensionDeltas {
			fmt.Fprintf(&b, "| %s | %s |\n", dim, fmtDelta(delta, "", true))
		}
	}

	if r.EvoSnapshot != nil {
		b.WriteString("\n### Evolution Snapshot Delta\n\n")
		b.WriteString("| Field | Delta |\n")
		b.WriteString("|-------|-------|\n")
		fmt.Fprintf(&b, "| Preference Count | %+d |\n", r.EvoSnapshot.PreferenceCountDelta)
		fmt.Fprintf(&b, "| Strategy Version | %+d |\n", r.EvoSnapshot.StrategyVersionDelta)
		fmt.Fprintf(&b, "| Skill Draft Count | %+d |\n", r.EvoSnapshot.SkillDraftCountDelta)
		fmt.Fprintf(&b, "| Trajectory Count | %+d |\n", r.EvoSnapshot.TrajectoryCountDelta)
		if len(r.EvoSnapshot.RouterModelChanges) > 0 {
			b.WriteString("\n#### Router Model Changes\n\n")
			b.WriteString("| Model | Task Count Delta |\n")
			b.WriteString("|-------|------------------|\n")
			for model, delta := range r.EvoSnapshot.RouterModelChanges {
				fmt.Fprintf(&b, "| %s | %+d |\n", model, delta)
			}
		}
	}

	if len(r.FeatureStateDiff) > 0 {
		b.WriteString("\n### Feature State Differences\n\n")
		b.WriteString("The following features changed between runs. Score deltas may reflect configuration differences rather than agent improvements:\n\n")
		b.WriteString("| Feature | Change |\n")
		b.WriteString("|---------|--------|\n")
		for name, change := range r.FeatureStateDiff {
			fmt.Fprintf(&b, "| %s | %s |\n", name, change)
		}
	}

	return b.String()
}

// fmtDelta formats a delta value with a +/- sign and optional unit.
// higherIsBetter controls whether a positive delta shows as improvement.
func fmtDelta(v float64, unit string, higherIsBetter bool) string {
	sign := ""
	if v > 0 {
		sign = "+"
	}

	indicator := ""
	if v > 0.001 {
		if higherIsBetter {
			indicator = " (better)"
		} else {
			indicator = " (worse)"
		}
	} else if v < -0.001 {
		if higherIsBetter {
			indicator = " (worse)"
		} else {
			indicator = " (better)"
		}
	}

	return fmt.Sprintf("%s%.1f%s%s", sign, v, unit, indicator)
}
