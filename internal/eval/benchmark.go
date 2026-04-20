package eval

import (
	"encoding/json"
	"fmt"
	"os"
)

// BenchmarkAdapter loads external benchmark datasets into IronClaw's TaskCase format.
type BenchmarkAdapter interface {
	Name() string
	LoadTasks(path string) ([]TaskCase, error)
	FormatResult(results []EvalResult) ([]byte, error)
}

// BenchmarkComparison holds IronClaw's score alongside reference scores
// from known agents for a specific benchmark.
type BenchmarkComparison struct {
	BenchmarkName string           `json:"benchmark"`
	IronClawScore float64          `json:"ironclaw_score"`
	TotalTasks    int              `json:"total_tasks"`
	PassedTasks   int              `json:"passed_tasks"`
	References    []ReferenceScore `json:"references"`
}

// ReferenceScore captures a known agent's benchmark result for comparison.
type ReferenceScore struct {
	AgentName string  `json:"agent_name"`
	Score     float64 `json:"score"`
	Source    string  `json:"source"`
}

// ComputeBenchmarkComparison builds a comparison report from eval results.
func ComputeBenchmarkComparison(benchmarkName string, results []EvalResult, refs []ReferenceScore) *BenchmarkComparison {
	passed := 0
	totalScore := 0.0
	for _, r := range results {
		if r.Success || r.FinalScore >= 0.5 {
			passed++
		}
		totalScore += r.FinalScore
	}
	avgScore := 0.0
	if len(results) > 0 {
		avgScore = totalScore / float64(len(results))
	}
	return &BenchmarkComparison{
		BenchmarkName: benchmarkName,
		IronClawScore: avgScore,
		TotalTasks:    len(results),
		PassedTasks:   passed,
		References:    refs,
	}
}

// FormatComparisonMarkdown renders a benchmark comparison as Markdown.
func (c *BenchmarkComparison) FormatComparisonMarkdown() string {
	s := fmt.Sprintf("# Benchmark: %s\n\n", c.BenchmarkName)
	s += fmt.Sprintf("**IronClaw Score**: %.1f%% (%d/%d passed)\n\n", c.IronClawScore*100, c.PassedTasks, c.TotalTasks)

	if len(c.References) > 0 {
		s += "## Reference Scores\n\n"
		s += "| Agent | Score | Source |\n"
		s += "|-------|-------|--------|\n"
		for _, ref := range c.References {
			s += fmt.Sprintf("| %s | %.1f%% | %s |\n", ref.AgentName, ref.Score*100, ref.Source)
		}
	}
	return s
}

// SaveJSON writes the comparison to a JSON file.
func (c *BenchmarkComparison) SaveJSON(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal benchmark comparison: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// AllBenchmarkAdapters returns all registered benchmark adapters.
func AllBenchmarkAdapters() map[string]BenchmarkAdapter {
	return map[string]BenchmarkAdapter{
		"swe-bench": &SWEBenchAdapter{},
		"humaneval":  &HumanEvalAdapter{},
		"gaia":       &GAIAAdapter{},
	}
}
