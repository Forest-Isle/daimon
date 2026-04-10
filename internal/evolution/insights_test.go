package evolution

import (
	"strings"
	"testing"
	"time"
)

func makeTrajectory(succeeded bool, tools []string, complexity string, replanCount int, feedback float64) TrajectoryRecord {
	var toolRecs []ToolRecord
	for _, t := range tools {
		toolRecs = append(toolRecs, ToolRecord{Name: t, Succeeded: succeeded, DurationMs: 100})
	}
	return TrajectoryRecord{
		SessionID:    "s1",
		Goal:         "test",
		Complexity:   complexity,
		Tools:        toolRecs,
		Reflection:   ReflectionBrief{Confidence: 0.8, Succeeded: succeeded},
		UserFeedback: feedback,
		ReplanCount:  replanCount,
		DurationMs:   500,
		Timestamp:    time.Now(),
	}
}

func TestGenerateInsights_Empty(t *testing.T) {
	report := GenerateInsights(nil, "test")
	if report.TotalEpisodes != 0 {
		t.Errorf("expected 0 episodes, got %d", report.TotalEpisodes)
	}
}

func TestGenerateInsights_BasicMetrics(t *testing.T) {
	records := []TrajectoryRecord{
		makeTrajectory(true, []string{"bash", "file"}, "moderate", 0, 1.0),
		makeTrajectory(true, []string{"bash"}, "simple", 0, 0.5),
		makeTrajectory(false, []string{"http"}, "complex", 2, -1.0),
	}

	report := GenerateInsights(records, "test period")

	if report.TotalEpisodes != 3 {
		t.Errorf("episodes = %d, want 3", report.TotalEpisodes)
	}
	// 2 out of 3 succeeded
	expectedSR := 2.0 / 3.0
	if abs(report.SuccessRate-expectedSR) > 0.01 {
		t.Errorf("success rate = %.3f, want %.3f", report.SuccessRate, expectedSR)
	}
	if report.Period != "test period" {
		t.Errorf("period = %q, want 'test period'", report.Period)
	}
}

func TestGenerateInsights_TopTools(t *testing.T) {
	records := []TrajectoryRecord{
		makeTrajectory(true, []string{"bash", "file"}, "moderate", 0, 0),
		makeTrajectory(true, []string{"bash", "http"}, "moderate", 0, 0),
		makeTrajectory(true, []string{"bash"}, "simple", 0, 0),
	}

	report := GenerateInsights(records, "test")

	if len(report.TopTools) == 0 {
		t.Fatal("expected at least one tool insight")
	}
	if report.TopTools[0].Name != "bash" {
		t.Errorf("top tool = %q, want bash", report.TopTools[0].Name)
	}
	if report.TopTools[0].Uses != 3 {
		t.Errorf("bash uses = %d, want 3", report.TopTools[0].Uses)
	}
}

func TestGenerateInsights_ComplexityStats(t *testing.T) {
	records := []TrajectoryRecord{
		makeTrajectory(true, []string{"bash"}, "simple", 0, 0),
		makeTrajectory(true, []string{"bash"}, "simple", 0, 0),
		makeTrajectory(false, []string{"bash"}, "complex", 0, 0),
	}

	report := GenerateInsights(records, "test")

	if len(report.ComplexityStats) != 2 {
		t.Fatalf("expected 2 complexity levels, got %d", len(report.ComplexityStats))
	}
}

func TestGenerateInsights_FailurePatterns(t *testing.T) {
	records := []TrajectoryRecord{
		makeTrajectory(false, []string{"bash", "http"}, "complex", 0, 0),
		makeTrajectory(false, []string{"bash", "http"}, "complex", 0, 0),
		makeTrajectory(true, []string{"bash"}, "simple", 0, 0),
	}

	report := GenerateInsights(records, "test")

	if len(report.FailurePatterns) != 1 {
		t.Fatalf("expected 1 failure pattern, got %d", len(report.FailurePatterns))
	}
	if report.FailurePatterns[0].Occurrences != 2 {
		t.Errorf("occurrences = %d, want 2", report.FailurePatterns[0].Occurrences)
	}
}

func TestGenerateInsights_Recommendations(t *testing.T) {
	var records []TrajectoryRecord
	for i := 0; i < 6; i++ {
		records = append(records, makeTrajectory(false, []string{"buggy_tool"}, "complex", 3, -0.5))
	}

	report := GenerateInsights(records, "test")

	if len(report.Recommendations) == 0 {
		t.Error("expected at least one recommendation for low success rate")
	}
}

func TestInsightsReport_FormatMarkdown(t *testing.T) {
	records := []TrajectoryRecord{
		makeTrajectory(true, []string{"bash", "file"}, "moderate", 0, 1.0),
		makeTrajectory(false, []string{"http"}, "complex", 1, -0.5),
	}

	report := GenerateInsights(records, "2026-04-10")
	md := report.FormatMarkdown()

	if !strings.Contains(md, "# IronClaw Insights") {
		t.Error("missing header")
	}
	if !strings.Contains(md, "Total episodes") {
		t.Error("missing summary table")
	}
	if !strings.Contains(md, "Tool Usage") {
		t.Error("missing tool usage section")
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		ms       int64
		expected string
	}{
		{500, "500ms"},
		{1500, "1.5s"},
		{65000, "1m5s"},
	}
	for _, tc := range tests {
		got := formatDuration(tc.ms)
		if got != tc.expected {
			t.Errorf("formatDuration(%d) = %q, want %q", tc.ms, got, tc.expected)
		}
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
