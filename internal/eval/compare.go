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
	GeneratedAt     time.Time             `json:"generated_at"`
}

// ComparisonDelta holds the differences between two runs.
type ComparisonDelta struct {
	SuccessRateDelta        float64 `json:"success_rate_delta"`
	AvgAssertPassRateDelta  float64 `json:"avg_assert_pass_rate_delta"`
	AvgConfidenceDelta      float64 `json:"avg_confidence_delta"`
	AvgReplanCountDelta     float64 `json:"avg_replan_count_delta"`
	DurationDelta           time.Duration `json:"duration_delta_ms"`
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

			if status == "regressed" {
				report.Regressions = append(report.Regressions, tr)
			} else if status == "improved" {
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
