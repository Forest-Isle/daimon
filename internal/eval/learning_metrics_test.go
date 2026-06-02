package eval

import (
	"math"
	"strings"
	"testing"
	"time"
)

// makeTestLMPoints builds IterationPoints with controlled reward/success/threshold values.
func makeTestLMPoints(n int, rewardFn, successFn, threshFn func(i int) float64, skillFn, prefFn func(i int) int) []IterationPoint {
	points := make([]IterationPoint, n)
	for i := 0; i < n; i++ {
		points[i] = IterationPoint{
			Iteration:       i + 1,
			RunID:           "iter-" + strings.Repeat("0", 3-len(string(rune('0'+i)))) + string(rune('0'+i)),
			Timestamp:       time.Now(),
			ReplanThreshold: threshFn(i),
			SkillDraftCount: skillFn(i),
			PreferenceCount: prefFn(i),
			Summary: SuiteSummary{
				SuccessRate: successFn(i),
			},
		}
	}
	return points
}

func TestComputeLearningCurve_Improving(t *testing.T) {
	points := makeTestLMPoints(5,
		func(i int) float64 { return float64(i) * 0.1 },  // 0, 0.1, 0.2, 0.3, 0.4
		func(i int) float64 { return float64(i) * 0.15 }, // 0, 0.15, 0.3, 0.45, 0.6
		func(i int) float64 { return 0.3 },
		func(i int) int { return i * 2 }, // 0,2,4,6,8
		func(i int) int { return i * 3 }, // 0,3,6,9,12
	)
	curve := ComputeLearningCurve(points)
	if curve == nil {
		t.Fatal("expected non-nil curve")
	}
	if curve.SuccessVelocity != VelocityImproving {
		t.Errorf("expected improving success velocity, got %s (slope=%.4f)", curve.SuccessVelocity, curve.SuccessRateSlope)
	}
	if curve.SuccessVelocity != VelocityImproving {
		t.Errorf("expected improving success velocity, got %s", curve.SuccessVelocity)
	}
	if curve.SkillGrowthPerIter <= 0 {
		t.Errorf("expected positive skill growth, got %.2f", curve.SkillGrowthPerIter)
	}
	if curve.PreferenceGrowthPerIter <= 0 {
		t.Errorf("expected positive preference growth, got %.2f", curve.PreferenceGrowthPerIter)
	}
}

func TestComputeLearningCurve_Degrading(t *testing.T) {
	points := makeTestLMPoints(4,
		func(i int) float64 { return 0.8 - float64(i)*0.1 }, // 0.8, 0.7, 0.6, 0.5
		func(i int) float64 { return 0.9 - float64(i)*0.1 },
		func(i int) float64 { return 0.3 },
		func(i int) int { return 0 },
		func(i int) int { return 0 },
	)
	curve := ComputeLearningCurve(points)
	if curve == nil {
		t.Fatal("expected non-nil curve for 4 points")
	}
	if curve.SuccessVelocity != VelocityDegrading {
		t.Errorf("expected degrading success, got %s (slope=%.4f)", curve.SuccessVelocity, curve.SuccessRateSlope)
	}
}

func TestComputeLearningCurve_InsufficientData(t *testing.T) {
	if ComputeLearningCurve(nil) != nil {
		t.Error("expected nil for empty points")
	}
	if ComputeLearningCurve([]IterationPoint{{}}) != nil {
		t.Error("expected nil for single point")
	}
}

func TestComputeLearningCurve_IterationCount(t *testing.T) {
	points := makeTestLMPoints(3,
		func(i int) float64 { return 0.5 },
		func(i int) float64 { return 0.5 },
		func(i int) float64 { return 0.3 },
		func(i int) int { return 0 },
		func(i int) int { return 0 },
	)
	curve := ComputeLearningCurve(points)
	if curve == nil {
		t.Fatal("expected non-nil curve")
	}
	if curve.IterationCount != 3 {
		t.Errorf("expected IterationCount=3, got %d", curve.IterationCount)
	}
}

func TestComputeStrategyConvergence_Converged(t *testing.T) {
	points := makeTestLMPoints(4,
		func(i int) float64 { return 0.5 },
		func(i int) float64 { return 0.7 },
		func(i int) float64 { return 0.30 + float64(i)*0.001 }, // nearly constant
		func(i int) int { return 0 },
		func(i int) int { return 0 },
	)
	conv := ComputeStrategyConvergence(points)
	if conv == nil {
		t.Fatal("expected non-nil convergence")
	}
	if !conv.IsConverged {
		t.Errorf("expected converged=true, oscillation=%.4f", conv.OscillationScore)
	}
}

func TestComputeStrategyConvergence_Oscillating(t *testing.T) {
	points := makeTestLMPoints(4,
		func(i int) float64 { return 0.5 },
		func(i int) float64 { return 0.5 },
		func(i int) float64 {
			// Alternating high oscillation: 0.2, 0.8, 0.2, 0.8
			if i%2 == 0 {
				return 0.2
			}
			return 0.8
		},
		func(i int) int { return 0 },
		func(i int) int { return 0 },
	)
	conv := ComputeStrategyConvergence(points)
	if conv == nil {
		t.Fatal("expected non-nil convergence")
	}
	if conv.IsConverged {
		t.Errorf("expected converged=false for oscillating thresholds, oscillation=%.4f", conv.OscillationScore)
	}
}

func TestComputeStrategyConvergence_InsufficientData(t *testing.T) {
	if ComputeStrategyConvergence(nil) != nil {
		t.Error("expected nil for empty points")
	}
	if ComputeStrategyConvergence([]IterationPoint{{}}) != nil {
		t.Error("expected nil for single point")
	}
}

func TestLmLinearSlope_KnownSlope(t *testing.T) {
	// y = 2x for x in 0..4 → slope should be 2
	ys := []float64{0, 2, 4, 6, 8}
	got := lmLinearSlope(ys)
	if math.Abs(got-2.0) > 0.001 {
		t.Errorf("expected slope 2.0, got %.4f", got)
	}
}

func TestLmLinearSlope_Flat(t *testing.T) {
	ys := []float64{5, 5, 5, 5}
	got := lmLinearSlope(ys)
	if math.Abs(got) > 0.001 {
		t.Errorf("expected slope 0 for flat series, got %.4f", got)
	}
}

func TestLmLinearSlope_InsufficientData(t *testing.T) {
	if lmLinearSlope(nil) != 0 {
		t.Error("expected 0 for nil input")
	}
	if lmLinearSlope([]float64{1}) != 0 {
		t.Error("expected 0 for single value")
	}
}

func TestComputeSelfLearningAnalysis_CompositeScoreRange(t *testing.T) {
	points := makeTestLMPoints(3,
		func(i int) float64 { return float64(i) * 0.3 },
		func(i int) float64 { return float64(i) * 0.2 },
		func(i int) float64 { return 0.3 + float64(i)*0.001 },
		func(i int) int { return i },
		func(i int) int { return i },
	)
	analysis := ComputeSelfLearningAnalysis(points)
	if analysis == nil {
		t.Fatal("expected non-nil analysis")
	}
	if analysis.CompositeScore < 0 || analysis.CompositeScore > 1 {
		t.Errorf("composite score out of [0,1] range: %.4f", analysis.CompositeScore)
	}
}

func TestGenerateLearningCurveHTML_Empty(t *testing.T) {
	html := GenerateLearningCurveHTML(nil)
	if !strings.Contains(html, "<html>") && !strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("expected valid HTML for empty points")
	}
}

func TestGenerateLearningCurveHTML_WithData(t *testing.T) {
	points := makeTestLMPoints(3,
		func(i int) float64 { return float64(i) * 0.1 },
		func(i int) float64 { return float64(i) * 0.2 },
		func(i int) float64 { return 0.3 },
		func(i int) int { return i * 2 },
		func(i int) int { return i * 3 },
	)
	html := GenerateLearningCurveHTML(points)
	if !strings.Contains(html, "successChart") {
		t.Error("expected success chart canvas in HTML")
	}
	if !strings.Contains(html, "chart.js") {
		t.Error("expected chart.js CDN reference in HTML")
	}
}

func TestFormatLearningCurveSummary_Nil(t *testing.T) {
	out := FormatLearningCurveSummary(nil)
	if !strings.Contains(out, "No self-learning analysis") && !strings.Contains(out, "insufficient") {
		t.Errorf("expected 'No self-learning analysis' or 'insufficient' in nil summary output, got: %q", out)
	}
}

func TestFormatLearningCurveSummary_WithData(t *testing.T) {
	points := makeTestLMPoints(3,
		func(i int) float64 { return float64(i) * 0.2 },
		func(i int) float64 { return float64(i) * 0.3 },
		func(i int) float64 { return 0.3 },
		func(i int) int { return i * 2 },
		func(i int) int { return i * 3 },
	)
	analysis := ComputeSelfLearningAnalysis(points)
	out := FormatLearningCurveSummary(analysis)
	if !strings.Contains(out, "Success rate") {
		t.Errorf("expected 'Success rate' in summary, got: %q", out)
	}
	if !strings.Contains(out, "Composite") {
		t.Errorf("expected 'Composite' score in summary, got: %q", out)
	}
}

func TestNewLongitudinalReportAutoAnalysis(t *testing.T) {
	points := makeTestLMPoints(3,
		func(i int) float64 { return float64(i) * 0.1 },
		func(i int) float64 { return float64(i) * 0.2 },
		func(i int) float64 { return 0.3 },
		func(i int) int { return 0 },
		func(i int) int { return 0 },
	)
	report := NewLongitudinalReport(points)
	if report.SelfLearningAnalysis == nil {
		t.Error("expected SelfLearningAnalysis to be auto-computed for ≥2 iteration points")
	}
}

func TestNewLongitudinalReportSinglePoint(t *testing.T) {
	points := makeTestLMPoints(1,
		func(i int) float64 { return 0.5 },
		func(i int) float64 { return 0.5 },
		func(i int) float64 { return 0.3 },
		func(i int) int { return 0 },
		func(i int) int { return 0 },
	)
	report := NewLongitudinalReport(points)
	if report.SelfLearningAnalysis != nil {
		t.Error("expected SelfLearningAnalysis to be nil for single point")
	}
}
