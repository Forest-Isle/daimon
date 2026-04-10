package evolution

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// InsightsReport summarizes patterns from trajectory data.
type InsightsReport struct {
	Period          string             `json:"period"`
	TotalEpisodes   int                `json:"total_episodes"`
	SuccessRate     float64            `json:"success_rate"`
	AvgDurationMs   int64              `json:"avg_duration_ms"`
	AvgReplanCount  float64            `json:"avg_replan_count"`
	AvgUserFeedback float64            `json:"avg_user_feedback"`
	TopTools        []ToolInsight      `json:"top_tools"`
	ComplexityStats []ComplexityStat   `json:"complexity_stats"`
	FailurePatterns []FailurePattern   `json:"failure_patterns,omitempty"`
	Recommendations []string           `json:"recommendations,omitempty"`
	GeneratedAt     time.Time          `json:"generated_at"`
}

// ToolInsight tracks per-tool statistics.
type ToolInsight struct {
	Name        string  `json:"name"`
	Uses        int     `json:"uses"`
	SuccessRate float64 `json:"success_rate"`
	AvgDuration int64   `json:"avg_duration_ms"`
}

// ComplexityStat tracks metrics per complexity level.
type ComplexityStat struct {
	Level       string  `json:"level"`
	Count       int     `json:"count"`
	SuccessRate float64 `json:"success_rate"`
}

// FailurePattern describes a recurring failure scenario.
type FailurePattern struct {
	Description string `json:"description"`
	Occurrences int    `json:"occurrences"`
}

// GenerateInsights analyzes trajectory records and produces an InsightsReport.
func GenerateInsights(records []TrajectoryRecord, periodLabel string) *InsightsReport {
	if len(records) == 0 {
		return &InsightsReport{
			Period:      periodLabel,
			GeneratedAt: time.Now(),
		}
	}

	report := &InsightsReport{
		Period:        periodLabel,
		TotalEpisodes: len(records),
		GeneratedAt:   time.Now(),
	}

	var (
		successes     int
		totalDuration int64
		totalReplans  int
		totalFeedback float64
		feedbackCount int
	)

	toolStats := make(map[string]*struct {
		uses, successes int
		totalDuration   int64
	})
	complexityBuckets := make(map[string]*struct {
		count, successes int
	})
	failedToolCombos := make(map[string]int)

	for _, rec := range records {
		if rec.Reflection.Succeeded {
			successes++
		}
		totalDuration += rec.DurationMs
		totalReplans += rec.ReplanCount
		if rec.UserFeedback != 0 {
			totalFeedback += rec.UserFeedback
			feedbackCount++
		}

		// Per-tool stats
		for _, tool := range rec.Tools {
			ts, ok := toolStats[tool.Name]
			if !ok {
				ts = &struct {
					uses, successes int
					totalDuration   int64
				}{}
				toolStats[tool.Name] = ts
			}
			ts.uses++
			if tool.Succeeded {
				ts.successes++
			}
			ts.totalDuration += tool.DurationMs
		}

		// Complexity stats
		cs, ok := complexityBuckets[rec.Complexity]
		if !ok {
			cs = &struct{ count, successes int }{}
			complexityBuckets[rec.Complexity] = cs
		}
		cs.count++
		if rec.Reflection.Succeeded {
			cs.successes++
		}

		// Failed tool combos
		if !rec.Reflection.Succeeded && len(rec.Tools) > 0 {
			names := make([]string, 0, len(rec.Tools))
			for _, t := range rec.Tools {
				names = append(names, t.Name)
			}
			sort.Strings(names)
			key := strings.Join(names, "+")
			failedToolCombos[key]++
		}
	}

	n := float64(len(records))
	report.SuccessRate = float64(successes) / n
	report.AvgDurationMs = totalDuration / int64(len(records))
	report.AvgReplanCount = float64(totalReplans) / n
	if feedbackCount > 0 {
		report.AvgUserFeedback = totalFeedback / float64(feedbackCount)
	}

	// Top tools by usage
	for name, ts := range toolStats {
		sr := 0.0
		if ts.uses > 0 {
			sr = float64(ts.successes) / float64(ts.uses)
		}
		avg := int64(0)
		if ts.uses > 0 {
			avg = ts.totalDuration / int64(ts.uses)
		}
		report.TopTools = append(report.TopTools, ToolInsight{
			Name:        name,
			Uses:        ts.uses,
			SuccessRate: sr,
			AvgDuration: avg,
		})
	}
	sort.Slice(report.TopTools, func(i, j int) bool {
		return report.TopTools[i].Uses > report.TopTools[j].Uses
	})
	if len(report.TopTools) > 10 {
		report.TopTools = report.TopTools[:10]
	}

	// Complexity stats
	for level, cs := range complexityBuckets {
		sr := 0.0
		if cs.count > 0 {
			sr = float64(cs.successes) / float64(cs.count)
		}
		report.ComplexityStats = append(report.ComplexityStats, ComplexityStat{
			Level:       level,
			Count:       cs.count,
			SuccessRate: sr,
		})
	}
	sort.Slice(report.ComplexityStats, func(i, j int) bool {
		return report.ComplexityStats[i].Count > report.ComplexityStats[j].Count
	})

	// Failure patterns (tool combos that fail >= 2 times)
	for combo, count := range failedToolCombos {
		if count >= 2 {
			report.FailurePatterns = append(report.FailurePatterns, FailurePattern{
				Description: fmt.Sprintf("tool combo [%s] failed", combo),
				Occurrences: count,
			})
		}
	}
	sort.Slice(report.FailurePatterns, func(i, j int) bool {
		return report.FailurePatterns[i].Occurrences > report.FailurePatterns[j].Occurrences
	})

	// Generate recommendations
	report.Recommendations = generateRecommendations(report)

	return report
}

func generateRecommendations(r *InsightsReport) []string {
	var recs []string

	if r.SuccessRate < 0.5 && r.TotalEpisodes >= 5 {
		recs = append(recs, fmt.Sprintf(
			"Overall success rate is low (%.0f%%). Consider reviewing common failure patterns.",
			r.SuccessRate*100))
	}

	for _, tool := range r.TopTools {
		if tool.Uses >= 3 && tool.SuccessRate < 0.5 {
			recs = append(recs, fmt.Sprintf(
				"Tool '%s' has a low success rate (%.0f%% across %d uses). Consider reviewing its usage patterns.",
				tool.Name, tool.SuccessRate*100, tool.Uses))
		}
	}

	if r.AvgReplanCount > 1.5 && r.TotalEpisodes >= 5 {
		recs = append(recs, fmt.Sprintf(
			"High average replan count (%.1f). Plans may be overly ambitious or confidence thresholds too low.",
			r.AvgReplanCount))
	}

	for _, cs := range r.ComplexityStats {
		if cs.Count >= 3 && cs.SuccessRate < 0.3 {
			recs = append(recs, fmt.Sprintf(
				"'%s' complexity tasks succeed only %.0f%% of the time. Consider breaking them into simpler subtasks.",
				cs.Level, cs.SuccessRate*100))
		}
	}

	if r.AvgUserFeedback < -0.3 && r.TotalEpisodes >= 5 {
		recs = append(recs, "Average user feedback is negative. Review recent sessions for quality issues.")
	}

	return recs
}

// FormatMarkdown renders the report as a human-readable Markdown string.
func (r *InsightsReport) FormatMarkdown() string {
	var b strings.Builder

	fmt.Fprintf(&b, "# IronClaw Insights — %s\n\n", r.Period)
	fmt.Fprintf(&b, "Generated: %s\n\n", r.GeneratedAt.Format(time.RFC3339))

	b.WriteString("## Summary\n\n")
	b.WriteString("| Metric | Value |\n|--------|-------|\n")
	fmt.Fprintf(&b, "| Total episodes | %d |\n", r.TotalEpisodes)
	fmt.Fprintf(&b, "| Success rate | %.1f%% |\n", r.SuccessRate*100)
	fmt.Fprintf(&b, "| Avg duration | %s |\n", formatDuration(r.AvgDurationMs))
	fmt.Fprintf(&b, "| Avg replans | %.1f |\n", r.AvgReplanCount)
	if r.AvgUserFeedback != 0 {
		fmt.Fprintf(&b, "| Avg user feedback | %.2f |\n", r.AvgUserFeedback)
	}
	b.WriteString("\n")

	if len(r.TopTools) > 0 {
		b.WriteString("## Tool Usage\n\n")
		b.WriteString("| Tool | Uses | Success Rate | Avg Duration |\n")
		b.WriteString("|------|------|-------------|-------------|\n")
		for _, t := range r.TopTools {
			fmt.Fprintf(&b, "| %s | %d | %.0f%% | %s |\n",
				t.Name, t.Uses, t.SuccessRate*100, formatDuration(t.AvgDuration))
		}
		b.WriteString("\n")
	}

	if len(r.ComplexityStats) > 0 {
		b.WriteString("## By Complexity\n\n")
		b.WriteString("| Level | Count | Success Rate |\n")
		b.WriteString("|-------|-------|--------------|\n")
		for _, cs := range r.ComplexityStats {
			fmt.Fprintf(&b, "| %s | %d | %.0f%% |\n",
				cs.Level, cs.Count, cs.SuccessRate*100)
		}
		b.WriteString("\n")
	}

	if len(r.FailurePatterns) > 0 {
		b.WriteString("## Failure Patterns\n\n")
		for _, fp := range r.FailurePatterns {
			fmt.Fprintf(&b, "- %s (%d occurrences)\n", fp.Description, fp.Occurrences)
		}
		b.WriteString("\n")
	}

	if len(r.Recommendations) > 0 {
		b.WriteString("## Recommendations\n\n")
		for _, rec := range r.Recommendations {
			fmt.Fprintf(&b, "- %s\n", rec)
		}
		b.WriteString("\n")
	}

	return b.String()
}

func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	s := float64(ms) / 1000
	if s < 60 {
		return fmt.Sprintf("%.1fs", s)
	}
	m := math.Floor(s / 60)
	return fmt.Sprintf("%.0fm%.0fs", m, s-m*60)
}
