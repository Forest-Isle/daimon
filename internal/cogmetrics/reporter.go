package cogmetrics

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// FormatMarkdown renders the health report as a human-readable Markdown block.
func (r *HealthReport) FormatMarkdown() string {
	var b strings.Builder

	b.WriteString("# Cognitive Health Report\n\n")
	fmt.Fprintf(&b, "**Uptime**: %s | **Episodes**: %d | **Reflections**: %d | **Strategy v%d**\n\n",
		r.Uptime.Truncate(1e9), r.TotalEpisodes, r.TotalReflections, r.StrategyVersion)

	b.WriteString("## Core Metrics\n\n")
	b.WriteString("| Metric | Value | Samples |\n")
	b.WriteString("|--------|-------|---------|\n")
	fmtMetricRow(&b, "Assertion Pass Rate", r.AssertionPassRate, true)
	fmtMetricRow(&b, "Replan Rate", r.ReplanRate, true)
	fmtMetricRow(&b, "Avg Confidence", r.AvgConfidence, false)

	b.WriteString("\n## Replan Efficiency\n\n")
	b.WriteString("| Condition | Success Rate | Samples |\n")
	b.WriteString("|-----------|-------------|---------|\n")
	fmtMetricRow(&b, "With Replan", r.ReplanEfficiency.WithReplan, true)
	fmtMetricRow(&b, "Without Replan", r.ReplanEfficiency.WithoutReplan, true)

	if len(r.ToolReliability) > 0 {
		b.WriteString("\n## Tool Reliability\n\n")
		b.WriteString("| Tool | Success Rate | Samples |\n")
		b.WriteString("|------|-------------|---------|\n")

		names := sortedKeys(r.ToolReliability)
		for _, name := range names {
			fmtMetricRow(&b, name, r.ToolReliability[name], true)
		}
	}

	if len(r.ComplexitySuccess) > 0 {
		b.WriteString("\n## Complexity Success Rates\n\n")
		b.WriteString("| Level | Success Rate | Samples |\n")
		b.WriteString("|-------|-------------|---------|\n")

		levels := sortedKeys(r.ComplexitySuccess)
		for _, level := range levels {
			fmtMetricRow(&b, level, r.ComplexitySuccess[level], true)
		}
	}

	return b.String()
}

func fmtMetricRow(b *strings.Builder, label string, mv MetricValue, asPercent bool) {
	if asPercent {
		fmt.Fprintf(b, "| %s | %.1f%% | %d |\n", label, mv.Value*100, mv.Samples)
	} else {
		fmt.Fprintf(b, "| %s | %.3f | %d |\n", label, mv.Value, mv.Samples)
	}
}

// FormatJSON renders the health report as indented JSON.
func (r *HealthReport) FormatJSON() (string, error) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func sortedKeys(m map[string]MetricValue) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
