package eval

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// WeaknessReport is the top-level output of the diagnosis engine.
type WeaknessReport struct {
	GeneratedAt     time.Time        `json:"generated_at"`
	OverallScore    float64          `json:"overall_score"`
	TotalTasks      int              `json:"total_tasks"`
	FailedTasks     int              `json:"failed_tasks"`
	DimReport       *DimensionReport `json:"dimension_report"`
	Weaknesses      []Weakness       `json:"weaknesses"`
	Recommendations []Recommendation `json:"recommendations"`
}

// Weakness describes a specific identified problem area.
type Weakness struct {
	ID          string          `json:"id"`
	Severity    string          `json:"severity"` // "critical", "major", "minor"
	Category    FailureCategory `json:"category"`
	Dimension   Dimension       `json:"dimension"`
	Description string          `json:"description"`
	Evidence    []string        `json:"evidence"`
	Frequency   int             `json:"frequency"`
}

// Recommendation provides an actionable optimization suggestion.
type Recommendation struct {
	TargetWeakness string `json:"target_weakness"`
	Priority       int    `json:"priority"`
	Action         string `json:"action"`
	Component      string `json:"component"`
	Detail         string `json:"detail"`
}

// DiagnoseOptions configures the diagnosis pipeline.
type DiagnoseOptions struct {
	Classifier *FailureClassifier
	Tasks      []TaskCase
}

// Diagnose analyzes a SuiteResult and produces a WeaknessReport.
func Diagnose(ctx context.Context, suite *SuiteResult, opts *DiagnoseOptions) *WeaknessReport {
	results := suite.Results

	if opts != nil && opts.Classifier != nil && opts.Tasks != nil {
		results = opts.Classifier.ClassifyAll(ctx, opts.Tasks, results)
	}

	dimReport := AggregateDimensions(results)

	var totalScore float64
	failCount := 0
	for _, r := range results {
		totalScore += r.FinalScore
		if !r.Success || r.FinalScore < 0.5 {
			failCount++
		}
	}
	overallScore := 0.0
	if len(results) > 0 {
		overallScore = totalScore / float64(len(results))
	}

	weaknesses := identifyWeaknesses(results, dimReport)
	recommendations := generateRecommendations(weaknesses)

	return &WeaknessReport{
		GeneratedAt:     time.Now(),
		OverallScore:    overallScore,
		TotalTasks:      len(results),
		FailedTasks:     failCount,
		DimReport:       dimReport,
		Weaknesses:      weaknesses,
		Recommendations: recommendations,
	}
}

func identifyWeaknesses(results []EvalResult, dimReport *DimensionReport) []Weakness {
	type key struct {
		cat FailureCategory
		dim Dimension
	}
	groups := make(map[key][]string)

	for _, r := range results {
		if r.FailureCategory == "" {
			continue
		}
		k := key{FailureCategory(r.FailureCategory), DefaultDimension(r.Dimension)}
		groups[k] = append(groups[k], r.TaskID)
	}

	for _, ds := range dimReport.Weakest {
		k := key{FailPlanningError, ds.Dimension}
		if ds.Dimension != DimPlanning {
			k.cat = FailIncompleteAnswer
		}
		if len(ds.TopFailures) > 0 {
			k.cat = ds.TopFailures[0]
		}
		if _, exists := groups[k]; !exists {
			var evidence []string
			for _, r := range results {
				if DefaultDimension(r.Dimension) == ds.Dimension && r.FinalScore < 0.7 {
					evidence = append(evidence, r.TaskID)
				}
			}
			if len(evidence) > 0 {
				groups[k] = evidence
			}
		}
	}

	var weaknesses []Weakness
	idx := 1
	for k, evidence := range groups {
		severity := "minor"
		if len(evidence) >= 3 {
			severity = "critical"
		} else if len(evidence) >= 2 {
			severity = "major"
		}

		desc := buildWeaknessDescription(k.cat, k.dim, len(evidence))

		weaknesses = append(weaknesses, Weakness{
			ID:          fmt.Sprintf("W-%03d", idx),
			Severity:    severity,
			Category:    k.cat,
			Dimension:   k.dim,
			Description: desc,
			Evidence:    evidence,
			Frequency:   len(evidence),
		})
		idx++
	}

	sort.Slice(weaknesses, func(i, j int) bool {
		sevOrder := map[string]int{"critical": 0, "major": 1, "minor": 2}
		if sevOrder[weaknesses[i].Severity] != sevOrder[weaknesses[j].Severity] {
			return sevOrder[weaknesses[i].Severity] < sevOrder[weaknesses[j].Severity]
		}
		return weaknesses[i].Frequency > weaknesses[j].Frequency
	})

	for i := range weaknesses {
		weaknesses[i].ID = fmt.Sprintf("W-%03d", i+1)
	}

	return weaknesses
}

func buildWeaknessDescription(cat FailureCategory, dim Dimension, freq int) string {
	descriptions := map[FailureCategory]string{
		FailPlanningError:    "Agent fails to properly decompose or sequence tasks",
		FailToolMisuse:       "Agent selects inappropriate tools for the task",
		FailToolMissing:      "Agent needs tools that are not available",
		FailErrorNoRecovery:  "Agent does not attempt recovery when encountering errors",
		FailErrorLoopRetry:   "Agent gets stuck in retry loops without making progress",
		FailHallucination:    "Agent generates information that contradicts ground truth",
		FailIncompleteAnswer: "Agent provides answers that lack required details",
		FailWrongAnswer:      "Agent produces factually incorrect results",
		FailTimeout:          "Agent exceeds time limits on task completion",
		FailContextLost:      "Agent loses track of conversation or task context",
		FailOverEngineering:  "Agent uses excessive complexity for simple tasks",
		FailUnknown:          "Agent fails for unidentified reasons",
	}

	base := descriptions[cat]
	if base == "" {
		base = string(cat)
	}

	return fmt.Sprintf("%s in %s dimension (%d occurrences)", base, dim, freq)
}

var builtinRecommendations = map[FailureCategory]struct {
	action    string
	component string
	detail    string
}{
	FailErrorLoopRetry: {
		action:    "Reduce maxReplans and improve reflect prompt",
		component: "agent/unified_loop.go (reflect phase)",
		detail:    "Check maxReplans configuration; optimize reflect prompt to better analyze failure causes and avoid repeating the same strategy",
	},
	FailToolMisuse: {
		action:    "Enhance tool descriptions in perceive/plan_task phase",
		component: "agent/unified_loop.go (plan_task), evolution/preference.go",
		detail:    "Strengthen tool description injection during perceive; leverage evolution engine's tool preference learning to guide selection",
	},
	FailHallucination: {
		action:    "Strengthen observe assertions and system prompt guardrails",
		component: "agent/unified_loop.go (observe phase), system prompt",
		detail:    "Enhance observe phase assertions; add explicit instruction in system prompt: 'When uncertain, say you don't know rather than guessing'",
	},
	FailPlanningError: {
		action:    "Optimize plan_task prompt with examples",
		component: "agent/unified_loop.go (plan_task), agent/task_context.go",
		detail:    "Add structured planning examples to plan_task prompt; adjust task context allocation for complex task decomposition",
	},
	FailTimeout: {
		action:    "Review timeout configuration and enable task auto-splitting",
		component: "config (llm.timeout), agent/unified_loop.go",
		detail:    "Increase LLM timeout for complex tasks; consider auto-splitting complex tasks via SubAgentManager",
	},
	FailIncompleteAnswer: {
		action:    "Strengthen reflect completeness self-check",
		component: "agent/unified_loop.go (reflect phase)",
		detail:    "Add completeness verification to reflect prompt; require agent to check all sub-goals before declaring task complete",
	},
	FailContextLost: {
		action:    "Tune context compression strategy",
		component: "agent/context_manager.go",
		detail:    "Check CompressionPipeline thresholds; increase context budget for multi-turn conversations; consider reducing summarize aggressiveness",
	},
	FailOverEngineering: {
		action:    "Add YAGNI guidance to system prompt",
		component: "system prompt, agent/unified_loop.go (plan_task)",
		detail:    "Emphasize simplicity in system prompt and plan_task phase; add 'prefer the simplest approach' instruction",
	},
	FailErrorNoRecovery: {
		action:    "Improve error handling in DAG execution",
		component: "agent/act.go (DAG executor), tool/interceptor.go",
		detail:    "Add retry guidance to DAG executor; ensure error messages are propagated clearly for reflect to analyze",
	},
	FailWrongAnswer: {
		action:    "Add verification step before final answer",
		component: "agent/unified_loop.go (observe phase)",
		detail:    "Add explicit answer verification in observe; cross-check output against task requirements before declaring success",
	},
}

func generateRecommendations(weaknesses []Weakness) []Recommendation {
	var recs []Recommendation
	seen := make(map[FailureCategory]bool)

	for priority, w := range weaknesses {
		if seen[w.Category] {
			continue
		}
		seen[w.Category] = true

		if rule, ok := builtinRecommendations[w.Category]; ok {
			recs = append(recs, Recommendation{
				TargetWeakness: w.ID,
				Priority:       priority + 1,
				Action:         rule.action,
				Component:      rule.component,
				Detail:         rule.detail,
			})
		}
	}

	return recs
}

// FormatMarkdown renders the WeaknessReport as a human-readable Markdown document.
func (r *WeaknessReport) FormatMarkdown() string {
	var b strings.Builder

	b.WriteString("# IronClaw Agent Weakness Diagnosis Report\n\n")
	fmt.Fprintf(&b, "**Generated**: %s\n", r.GeneratedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "**Overall Score**: %.2f / 1.00\n", r.OverallScore)
	fmt.Fprintf(&b, "**Tasks**: %d total, %d failed\n\n", r.TotalTasks, r.FailedTasks)

	b.WriteString("## Dimension Scores\n\n")
	b.WriteString("| Dimension | Tasks | Success Rate | Avg Score | Avg Replan | Top Failures |\n")
	b.WriteString("|-----------|-------|-------------|-----------|------------|-------------|\n")
	if r.DimReport != nil {
		for _, ds := range r.DimReport.Dimensions {
			failures := "-"
			if len(ds.TopFailures) > 0 {
				parts := make([]string, len(ds.TopFailures))
				for i, f := range ds.TopFailures {
					parts[i] = string(f)
				}
				failures = strings.Join(parts, ", ")
			}
			fmt.Fprintf(&b, "| %s | %d | %.1f%% | %.2f | %.1f | %s |\n",
				ds.Dimension, ds.TaskCount, ds.SuccessRate*100,
				ds.AvgScore, ds.AvgReplan, failures)
		}
	}

	if len(r.Weaknesses) > 0 {
		b.WriteString("\n## Weaknesses (sorted by severity)\n\n")
		for _, w := range r.Weaknesses {
			fmt.Fprintf(&b, "### [%s] %s: %s\n", strings.ToUpper(w.Severity), w.ID, w.Description)
			fmt.Fprintf(&b, "- **Dimension**: %s\n", w.Dimension)
			fmt.Fprintf(&b, "- **Category**: %s\n", w.Category)
			fmt.Fprintf(&b, "- **Frequency**: %d\n", w.Frequency)
			fmt.Fprintf(&b, "- **Evidence**: %s\n\n", strings.Join(w.Evidence, ", "))
		}
	}

	if len(r.Recommendations) > 0 {
		b.WriteString("## Optimization Recommendations\n\n")
		b.WriteString("| Priority | Target | Action | Component | Detail |\n")
		b.WriteString("|----------|--------|--------|-----------|--------|\n")
		for _, rec := range r.Recommendations {
			fmt.Fprintf(&b, "| %d | %s | %s | %s | %s |\n",
				rec.Priority, rec.TargetWeakness, rec.Action, rec.Component, rec.Detail)
		}
	}

	if r.DimReport != nil && len(r.DimReport.FailureDistribution) > 0 {
		b.WriteString("\n## Failure Distribution\n\n")
		b.WriteString("| Category | Count |\n")
		b.WriteString("|----------|-------|\n")

		type kv struct {
			cat   FailureCategory
			count int
		}
		var sorted []kv
		for cat, cnt := range r.DimReport.FailureDistribution {
			sorted = append(sorted, kv{cat, cnt})
		}
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].count > sorted[j].count
		})
		for _, kv := range sorted {
			fmt.Fprintf(&b, "| %s | %d |\n", kv.cat, kv.count)
		}
	}

	return b.String()
}
