package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
)

// AdaptiveGenerator creates new evaluation tasks targeting identified weaknesses.
type AdaptiveGenerator struct {
	provider agent.Provider
}

// NewAdaptiveGenerator creates a generator that uses an LLM to create targeted tasks.
func NewAdaptiveGenerator(provider agent.Provider) *AdaptiveGenerator {
	return &AdaptiveGenerator{provider: provider}
}

// GeneratedTask wraps a TaskCase with metadata about why it was generated.
type GeneratedTask struct {
	TaskCase
	TargetWeakness string `json:"target_weakness"`
	Rationale      string `json:"rationale"`
}

// Generate creates new tasks targeting the top weaknesses in a report.
// Returns up to count tasks (2 per weakness, targeting top-3 weaknesses).
func (g *AdaptiveGenerator) Generate(ctx context.Context, report *WeaknessReport, count int) ([]GeneratedTask, error) {
	if report == nil || len(report.Weaknesses) == 0 {
		return nil, nil
	}
	if g.provider == nil {
		return nil, fmt.Errorf("adaptive generator requires an LLM provider")
	}

	topN := 3
	if len(report.Weaknesses) < topN {
		topN = len(report.Weaknesses)
	}
	perWeakness := max(1, count/topN)

	var allGenerated []GeneratedTask
	for _, w := range report.Weaknesses[:topN] {
		tasks, err := g.generateForWeakness(ctx, w, perWeakness)
		if err != nil {
			slog.Warn("adaptive: failed to generate for weakness", "weakness", w.ID, "err", err)
			continue
		}
		allGenerated = append(allGenerated, tasks...)
	}

	if len(allGenerated) > count {
		allGenerated = allGenerated[:count]
	}

	return allGenerated, nil
}

func (g *AdaptiveGenerator) generateForWeakness(ctx context.Context, w Weakness, count int) ([]GeneratedTask, error) {
	prompt := fmt.Sprintf(`Generate %d evaluation tasks targeting this weakness:

Weakness: %s
Category: %s
Dimension: %s
Description: %s
Evidence (failed tasks): %s

Each task must be a JSON object with these fields:
- id: unique string (prefix with "adaptive-")
- goal: clear task description for the agent
- complexity: "simple", "moderate", or "complex"
- tags: string array including "adaptive" and the dimension
- expect_tools: string array of expected tools
- dimension: "%s"
- verify_method: "deterministic", "llm_judge", or "hybrid"
- must_contain: string array (for deterministic verification, what the output must include)
- rationale: why this task tests the weakness

Respond with a JSON array of task objects. Tasks should be concrete and verifiable.`, count, w.ID, w.Category, w.Dimension, w.Description,
		strings.Join(w.Evidence, ", "), w.Dimension)

	resp, err := g.provider.Complete(ctx, agent.CompletionRequest{
		System:    "You are an evaluation task designer. Generate concrete, verifiable tasks that test specific agent weaknesses. Respond ONLY with a JSON array.",
		Messages:  []agent.CompletionMessage{{Role: "user", Content: prompt}},
		MaxTokens: 2048,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM generate: %w", err)
	}

	return g.parseTasks(resp.Text, w)
}

type rawGeneratedTask struct {
	ID           string   `json:"id"`
	Goal         string   `json:"goal"`
	Complexity   string   `json:"complexity"`
	Tags         []string `json:"tags"`
	ExpectTools  []string `json:"expect_tools"`
	Dimension    string   `json:"dimension"`
	VerifyMethod string   `json:"verify_method"`
	MustContain  []string `json:"must_contain"`
	Rationale    string   `json:"rationale"`
}

func (g *AdaptiveGenerator) parseTasks(text string, w Weakness) ([]GeneratedTask, error) {
	text = strings.TrimSpace(text)

	if idx := strings.Index(text, "```json"); idx >= 0 {
		text = text[idx+7:]
		if end := strings.Index(text, "```"); end >= 0 {
			text = text[:end]
		}
	} else if idx := strings.Index(text, "```"); idx >= 0 {
		text = text[idx+3:]
		if end := strings.Index(text, "```"); end >= 0 {
			text = text[:end]
		}
	}
	text = strings.TrimSpace(text)

	if start := strings.Index(text, "["); start >= 0 {
		depth := 0
		for i := start; i < len(text); i++ {
			if text[i] == '[' {
				depth++
			} else if text[i] == ']' {
				depth--
				if depth == 0 {
					text = text[start : i+1]
					break
				}
			}
		}
	}

	var raw []rawGeneratedTask
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		slog.Warn("adaptive: failed to parse LLM response", "err", err)
		return nil, fmt.Errorf("parse generated tasks: %w", err)
	}

	var tasks []GeneratedTask
	for _, r := range raw {
		tc := TaskCase{
			ID:           r.ID,
			Goal:         r.Goal,
			Complexity:   r.Complexity,
			Tags:         r.Tags,
			ExpectTools:  r.ExpectTools,
			Dimension:    Dimension(r.Dimension),
			VerifyMethod: VerifyMethod(r.VerifyMethod),
		}

		if len(r.MustContain) > 0 {
			tc.Reference = &Reference{MustContain: r.MustContain}
		}

		if tc.ID == "" {
			tc.ID = fmt.Sprintf("adaptive-%s-%d", w.ID, len(tasks))
		}
		if tc.Dimension == "" {
			tc.Dimension = w.Dimension
		}
		if tc.VerifyMethod == "" {
			tc.VerifyMethod = VerifyHybrid
		}

		tasks = append(tasks, GeneratedTask{
			TaskCase:       tc,
			TargetWeakness: w.ID,
			Rationale:      r.Rationale,
		})
	}

	return tasks, nil
}

// RoundSnapshot captures the state of one adaptive evaluation round.
type RoundSnapshot struct {
	Round          int       `json:"round"`
	RunID          string    `json:"run_id"`
	TaskCount      int       `json:"task_count"`
	OverallScore   float64   `json:"overall_score"`
	FailedTasks    int       `json:"failed_tasks"`
	WeaknessCount  int       `json:"weakness_count"`
	GeneratedCount int       `json:"generated_count"`
	TopWeaknesses  []string  `json:"top_weaknesses"`
	Timestamp      time.Time `json:"timestamp"`
}

// AdaptiveSummary captures the full multi-round adaptive evaluation.
type AdaptiveSummary struct {
	Rounds        []RoundSnapshot      `json:"rounds"`
	WeaknessTrend map[string][]float64 `json:"weakness_trend"`
	Converging    []string             `json:"converging"`
	Diverging     []string             `json:"diverging"`
	GeneratedAt   time.Time            `json:"generated_at"`
}

// AdaptiveLoopOptions configures the adaptive evaluation loop.
type AdaptiveLoopOptions struct {
	Suite         string
	Rounds        int
	TasksPerRound int
	Runner        AgentRunner
	Provider      agent.Provider
	OutputDir     string
}

// RunAdaptiveLoop executes the multi-round adaptive evaluation.
func RunAdaptiveLoop(ctx context.Context, baseTasks []TaskCase, opts AdaptiveLoopOptions) (*AdaptiveSummary, error) {
	if opts.Rounds <= 0 {
		opts.Rounds = 3
	}
	if opts.TasksPerRound <= 0 {
		opts.TasksPerRound = 6
	}

	generator := NewAdaptiveGenerator(opts.Provider)
	judge := NewLLMJudge(opts.Provider)
	classifier := NewFailureClassifier(opts.Provider, 5*time.Minute)

	currentTasks := make([]TaskCase, len(baseTasks))
	copy(currentTasks, baseTasks)

	summary := &AdaptiveSummary{
		WeaknessTrend: make(map[string][]float64),
	}

	dimScores := make(map[string][]float64)

	for round := 1; round <= opts.Rounds; round++ {
		runID := fmt.Sprintf("adaptive-r%d-%s", round, time.Now().Format("150405"))
		fmt.Printf("\n=== Adaptive Round %d/%d (run: %s, %d tasks) ===\n", round, opts.Rounds, runID, len(currentTasks))

		runOpts := &RunOptions{Judge: judge}
		suiteResult, err := RunSuiteWithOptions(ctx, runID, currentTasks, opts.Runner, runOpts)
		if err != nil {
			return summary, fmt.Errorf("round %d: run suite: %w", round, err)
		}

		report := Diagnose(ctx, suiteResult, &DiagnoseOptions{
			Classifier: classifier,
			Tasks:      currentTasks,
		})

		snap := RoundSnapshot{
			Round:         round,
			RunID:         runID,
			TaskCount:     len(currentTasks),
			OverallScore:  report.OverallScore,
			FailedTasks:   report.FailedTasks,
			WeaknessCount: len(report.Weaknesses),
			Timestamp:     time.Now(),
		}

		for _, w := range report.Weaknesses {
			if len(snap.TopWeaknesses) < 3 {
				snap.TopWeaknesses = append(snap.TopWeaknesses, fmt.Sprintf("%s(%s)", w.Category, w.Dimension))
			}
		}

		if report.DimReport != nil {
			for _, ds := range report.DimReport.Dimensions {
				key := string(ds.Dimension)
				dimScores[key] = append(dimScores[key], ds.AvgScore)
			}
		}

		if round < opts.Rounds && opts.Provider != nil {
			generated, genErr := generator.Generate(ctx, report, opts.TasksPerRound)
			if genErr != nil {
				slog.Warn("adaptive: generation failed", "round", round, "err", genErr)
			} else {
				snap.GeneratedCount = len(generated)
				for _, gt := range generated {
					currentTasks = append(currentTasks, gt.TaskCase)
				}
				fmt.Printf("  Generated %d adaptive tasks for next round\n", len(generated))
			}
		}

		summary.Rounds = append(summary.Rounds, snap)

		if opts.OutputDir != "" {
			roundDir := fmt.Sprintf("%s/round_%d", opts.OutputDir, round)
			_ = saveRoundArtifacts(roundDir, suiteResult, report)
		}

		fmt.Printf("  Score: %.2f | Failed: %d/%d | Weaknesses: %d\n",
			report.OverallScore, report.FailedTasks, len(currentTasks), len(report.Weaknesses))
	}

	summary.WeaknessTrend = dimScores
	for dim, scores := range dimScores {
		if len(scores) >= 2 {
			trend := scores[len(scores)-1] - scores[0]
			if trend > 0.05 {
				summary.Converging = append(summary.Converging, dim)
			} else if trend < -0.05 {
				summary.Diverging = append(summary.Diverging, dim)
			}
		}
	}

	summary.GeneratedAt = time.Now()
	return summary, nil
}

func saveRoundArtifacts(dir string, suite *SuiteResult, report *WeaknessReport) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	_ = suite.SaveJSON(dir + "/results.json")

	data, _ := json.MarshalIndent(report, "", "  ")
	_ = os.WriteFile(dir+"/weakness_report.json", data, 0o644)
	_ = os.WriteFile(dir+"/weakness_report.md", []byte(report.FormatMarkdown()), 0o644)
	return nil
}

// FormatMarkdown renders the summary as Markdown.
func (s *AdaptiveSummary) FormatMarkdown() string {
	var b strings.Builder

	b.WriteString("# Adaptive Evaluation Summary\n\n")
	fmt.Fprintf(&b, "**Generated**: %s\n", s.GeneratedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "**Rounds**: %d\n\n", len(s.Rounds))

	b.WriteString("## Round Progress\n\n")
	b.WriteString("| Round | Tasks | Score | Failed | Weaknesses | Generated |\n")
	b.WriteString("|-------|-------|-------|--------|------------|----------|\n")
	for _, r := range s.Rounds {
		fmt.Fprintf(&b, "| %d | %d | %.2f | %d | %d | %d |\n",
			r.Round, r.TaskCount, r.OverallScore, r.FailedTasks, r.WeaknessCount, r.GeneratedCount)
	}

	if len(s.Converging) > 0 {
		fmt.Fprintf(&b, "\n**Improving**: %s\n", strings.Join(s.Converging, ", "))
	}
	if len(s.Diverging) > 0 {
		fmt.Fprintf(&b, "**Declining**: %s\n", strings.Join(s.Diverging, ", "))
	}

	return b.String()
}
