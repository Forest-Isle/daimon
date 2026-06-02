package eval

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// LearningVelocity classifies the direction and magnitude of a learning metric trend.
type LearningVelocity string

const (
	VelocityImproving  LearningVelocity = "improving"
	VelocityDegrading  LearningVelocity = "degrading"
	VelocityStable     LearningVelocity = "stable"
	VelocityInsuffData LearningVelocity = "insufficient_data"
)

// LearningCurveAnalysis captures the trend in agent performance across
// longitudinal evaluation iterations using linear regression.
type LearningCurveAnalysis struct {
	// SuccessRateSlope is the linear regression slope of SuccessRate.
	SuccessRateSlope float64 `json:"success_rate_slope"`

	// SkillGrowthPerIter is the average SkillDraftCount increase per iteration.
	SkillGrowthPerIter float64 `json:"skill_growth_per_iter"`

	// PreferenceGrowthPerIter is the average PreferenceCount increase per iteration.
	PreferenceGrowthPerIter float64 `json:"preference_growth_per_iter"`

	// SuccessVelocity classifies the success rate trend direction.
	SuccessVelocity LearningVelocity `json:"success_velocity"`

	// IterationCount is the number of data points used.
	IterationCount int `json:"iteration_count"`

	// First/Last bounds for success rate.
	FirstSuccessRate float64 `json:"first_success_rate"`
	LastSuccessRate  float64 `json:"last_success_rate"`
}

// StrategyConvergenceAnalysis evaluates whether the agent's replan strategy
// is converging toward a stable value or oscillating.
type StrategyConvergenceAnalysis struct {
	// IsConverged is true when the oscillation score is below 0.02
	// (mean absolute change < 2% per iteration).
	IsConverged bool `json:"is_converged"`

	// ThresholdMean is the mean replan threshold across iterations.
	ThresholdMean float64 `json:"threshold_mean"`

	// ThresholdStdDev is the standard deviation of the replan threshold.
	ThresholdStdDev float64 `json:"threshold_std_dev"`

	// ThresholdTrend is "rising", "falling", or "stable".
	ThresholdTrend string `json:"threshold_trend"`

	// OscillationScore measures instability via mean absolute deviation of
	// successive threshold changes. High = oscillating, low = stable.
	OscillationScore float64 `json:"oscillation_score"`

	// IterationCount is the number of data points used.
	IterationCount int `json:"iteration_count"`
}

// SelfLearningAnalysisSummary is a compact summary of self-learning metrics
// suitable for embedding in LongitudinalReport and serializing to JSON.
type SelfLearningAnalysisSummary struct {
	LearningCurve       *LearningCurveAnalysis       `json:"learning_curve,omitempty"`
	StrategyConvergence *StrategyConvergenceAnalysis `json:"strategy_convergence,omitempty"`
	CompositeScore      float64                      `json:"composite_score"`
	GeneratedAt         time.Time                    `json:"generated_at"`
}

// ComputeLearningCurve derives learning curve analytics from a series of
// IterationPoints using linear regression. Requires at least 2 points.
func ComputeLearningCurve(points []IterationPoint) *LearningCurveAnalysis {
	if len(points) < 2 {
		return nil
	}

	n := float64(len(points))
	first := points[0]
	last := points[len(points)-1]

	successSlope := lmLinearSlope(lmExtractFloat(points, func(p IterationPoint) float64 { return p.Summary.SuccessRate }))

	skillGrowth := 0.0
	prefGrowth := 0.0
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
		SuccessRateSlope:        successSlope,
		SkillGrowthPerIter:      skillGrowth,
		PreferenceGrowthPerIter: prefGrowth,
		SuccessVelocity:         lmClassifyVelocity(successSlope, 0.005),
		IterationCount:          len(points),
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

	thresholds := lmExtractFloat(points, func(p IterationPoint) float64 { return p.ReplanThreshold })
	m := lmMean(thresholds)
	sd := lmStdDev(thresholds, m)

	var oscillation float64
	if len(thresholds) > 1 {
		sumDiff := 0.0
		for i := 1; i < len(thresholds); i++ {
			sumDiff += math.Abs(thresholds[i] - thresholds[i-1])
		}
		oscillation = sumDiff / float64(len(thresholds)-1)
	}

	slope := lmLinearSlope(thresholds)
	trend := "stable"
	if slope > 0.005 {
		trend = "rising"
	} else if slope < -0.005 {
		trend = "falling"
	}

	return &StrategyConvergenceAnalysis{
		IsConverged:      oscillation < 0.02,
		ThresholdMean:    m,
		ThresholdStdDev:  sd,
		ThresholdTrend:   trend,
		OscillationScore: oscillation,
		IterationCount:   len(points),
	}
}

// ComputeSelfLearningAnalysis computes the full self-learning summary from
// a series of IterationPoints. Safe to call with < 2 points (returns zero values).
func ComputeSelfLearningAnalysis(points []IterationPoint) *SelfLearningAnalysisSummary {
	curve := ComputeLearningCurve(points)
	convergence := ComputeStrategyConvergence(points)
	return &SelfLearningAnalysisSummary{
		LearningCurve:       curve,
		StrategyConvergence: convergence,
		CompositeScore:      lmCompositeScore(curve, convergence),
		GeneratedAt:         time.Now(),
	}
}

// FormatLearningCurveSummary returns a human-readable text summary of the
// self-learning analysis, suitable for CLI output.
func FormatLearningCurveSummary(s *SelfLearningAnalysisSummary) string {
	if s == nil {
		return "No self-learning analysis available (need >=2 longitudinal iterations).\n"
	}
	var b strings.Builder
	b.WriteString("\n=== Self-Learning Analysis ===\n")

	if s.LearningCurve != nil {
		c := s.LearningCurve
		fmt.Fprintf(&b, "Learning Curve (%d iterations):\n", c.IterationCount)
		fmt.Fprintf(&b, "  Success rate:   %.0f%% -> %.0f%%  slope=%.4f  [%s]\n",
			c.FirstSuccessRate*100, c.LastSuccessRate*100, c.SuccessRateSlope, c.SuccessVelocity)
		fmt.Fprintf(&b, "  Skill growth:   +%.2f/iter\n", c.SkillGrowthPerIter)
		fmt.Fprintf(&b, "  Pref growth:    +%.2f/iter\n", c.PreferenceGrowthPerIter)
	} else {
		b.WriteString("  Learning curve: insufficient data\n")
	}

	if s.StrategyConvergence != nil {
		sc := s.StrategyConvergence
		convergedStr := "NO"
		if sc.IsConverged {
			convergedStr = "YES"
		}
		fmt.Fprintf(&b, "Strategy Convergence: %s  oscillation=%.4f  trend=%s\n",
			convergedStr, sc.OscillationScore, sc.ThresholdTrend)
		fmt.Fprintf(&b, "  Threshold: mean=%.3f  stddev=%.3f\n",
			sc.ThresholdMean, sc.ThresholdStdDev)
	} else {
		b.WriteString("  Strategy convergence: insufficient data\n")
	}

	fmt.Fprintf(&b, "Composite self-learning score: %.2f / 1.00\n", s.CompositeScore)
	return b.String()
}

// GenerateLearningCurveHTML returns a self-contained HTML page with four line
// charts visualising learning curve data across longitudinal iterations.
// Uses Chart.js via CDN -- requires internet access to render.
func GenerateLearningCurveHTML(points []IterationPoint) string {
	if len(points) == 0 {
		return "<html><body><p>No iteration data available.</p></body></html>"
	}

	labels := make([]string, len(points))
	successRates := make([]float64, len(points))
	skillCounts := make([]int, len(points))
	prefCounts := make([]int, len(points))

	for i, p := range points {
		labels[i] = p.RunID
		successRates[i] = p.Summary.SuccessRate * 100
		skillCounts[i] = p.SkillDraftCount
		prefCounts[i] = p.PreferenceCount
	}

	analysis := ComputeSelfLearningAnalysis(points)
	summaryText := FormatLearningCurveSummary(analysis)
	summaryHTML := strings.ReplaceAll(strings.ReplaceAll(summaryText, "\n", "<br>"), "  ", "&nbsp;&nbsp;")

	return fmt.Sprintf(`<!DOCTYPE html>
	<html lang="en">
	<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>IronClaw -- Self-Learning Curve</title>
	<script src="https://cdn.jsdelivr.net/npm/chart.js@4/dist/chart.umd.min.js"></script>
	<style>
	body{font-family:system-ui,sans-serif;background:#0f1117;color:#e2e8f0;margin:0;padding:24px}
	h1{color:#a78bfa;margin-bottom:4px}
	.subtitle{color:#64748b;font-size:14px;margin-bottom:24px}
	.summary{background:#1e2130;border-left:3px solid #a78bfa;padding:16px;margin-bottom:28px;
	         font-family:monospace;font-size:13px;line-height:1.8;border-radius:4px;white-space:pre-wrap}
	.chart-grid{display:grid;grid-template-columns:1fr 1fr;gap:20px}
	.chart-box{background:#1e2130;border-radius:8px;padding:18px}
	canvas{max-height:260px}
	</style>
	</head>
	<body>
	<h1>&#129504; IronClaw &mdash; Self-Learning Curve</h1>
	<div class="subtitle">Generated %s &nbsp;|&nbsp; %d iterations</div>
	<div class="summary">%s</div>
	<div class="chart-grid">
	  <div class="chart-box"><canvas id="successChart"></canvas></div>
	  <div class="chart-box"><canvas id="skillChart"></canvas></div>
	  <div class="chart-box"><canvas id="prefChart"></canvas></div>
	</div>
	<script>
	const labels=%s,successRates=%s,skillCounts=%s,prefCounts=%s;
	const opts={responsive:true,plugins:{legend:{labels:{color:'#e2e8f0'}},
	  scales:{x:{ticks:{color:'#94a3b8'},grid:{color:'#2d3748'}},
	          y:{ticks:{color:'#94a3b8'},grid:{color:'#2d3748'}}}};
	function mkChart(id,label,data,color){
	  new Chart(document.getElementById(id),{type:'line',options:opts,
	    data:{labels,datasets:[{label,data,borderColor:color,backgroundColor:color+'33',
	      tension:0.3,fill:true,pointRadius:5}]}});
	}
	mkChart('successChart','Success Rate (%%)',successRates,'#34d399');
	mkChart('skillChart','Skill Draft Count',skillCounts,'#f59e0b');
	mkChart('prefChart','Preference Count',prefCounts,'#60a5fa');
	</script>
	</body>
	</html>`,
		time.Now().Format("2006-01-02 15:04"),
		len(points),
		summaryHTML,
		lmToJSStringArray(labels),
		lmToJSFloatArray(successRates),
		lmToJSIntArray(skillCounts),
		lmToJSIntArray(prefCounts),
	)
}

// --- private helpers (prefixed lm_ to avoid collisions with other eval files) ---

func lmExtractFloat(points []IterationPoint, f func(IterationPoint) float64) []float64 {
	out := make([]float64, len(points))
	for i, p := range points {
		out[i] = f(p)
	}
	return out
}

func lmMean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func lmStdDev(xs []float64, m float64) float64 {
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

// lmLinearSlope computes the least-squares slope for a sequence of values,
// treating each index as the x coordinate (0, 1, 2, ...).
func lmLinearSlope(ys []float64) float64 {
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

func lmClassifyVelocity(slope, threshold float64) LearningVelocity {
	if slope > threshold {
		return VelocityImproving
	}
	if slope < -threshold {
		return VelocityDegrading
	}
	return VelocityStable
}

func lmCompositeScore(curve *LearningCurveAnalysis, conv *StrategyConvergenceAnalysis) float64 {
	score := 0.5
	if curve != nil {
		switch curve.SuccessVelocity {
		case VelocityImproving:
			score += 0.4
		case VelocityDegrading:
			score -= 0.2
		}
	}
	if conv != nil && conv.IsConverged {
		score += 0.1
	}
	return math.Max(0, math.Min(1, score))
}

func lmToJSStringArray(ss []string) string {
	quoted := make([]string, len(ss))
	for i, s := range ss {
		quoted[i] = `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

func lmToJSFloatArray(fs []float64) string {
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = fmt.Sprintf("%.4f", f)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func lmToJSIntArray(is []int) string {
	parts := make([]string, len(is))
	for i, v := range is {
		parts[i] = fmt.Sprintf("%d", v)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
