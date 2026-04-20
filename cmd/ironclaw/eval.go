package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/eval"
	"github.com/Forest-Isle/IronClaw/internal/gateway"
	"github.com/Forest-Isle/IronClaw/internal/userdir"
	"github.com/spf13/cobra"
)

func newEvalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Evaluate agent performance with reproducible task suites",
	}
	cmd.AddCommand(newEvalRunCmd(), newEvalCompareCmd(), newEvalListCmd(), newEvalLongitudinalCmd(), newEvalVisualizeCmd(), newEvalDiagnoseCmd())
	return cmd
}

func newEvalRunCmd() *cobra.Command {
	var (
		suite      string
		output     string
		runID      string
		live       bool
		configPath string
		judge      bool
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run an evaluation suite and record results",
		Long: `Run an evaluation suite against the agent.

By default uses a DryRunner (no LLM calls). Use --live to run against a real
cognitive agent with full LLM integration. The --live flag requires a valid
config file with LLM credentials and agent.mode set to "cognitive".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			tasks, err := loadSuite(suite)
			if err != nil {
				return err
			}

			if runID == "" {
				runID = fmt.Sprintf("eval-%s", time.Now().Format("20060102-150405"))
			}

			var runner eval.AgentRunner
			var gw *gateway.Gateway

			if live {
				var cleanup func()
				gw, cleanup, err = initEvalGateway(configPath)
				if err != nil {
					return fmt.Errorf("init live eval: %w", err)
				}
				defer cleanup()

				r := gw.NewEvalRunner()
				if r == nil {
					return fmt.Errorf("live eval requires agent.mode = cognitive in config")
				}
				runner = r
				fmt.Printf("Starting LIVE evaluation run: %s (%d tasks)\n\n", runID, len(tasks))
			} else {
				runner = &eval.DryRunner{}
				fmt.Printf("Starting DRY evaluation run: %s (%d tasks)\n\n", runID, len(tasks))
			}

			var runOpts *eval.RunOptions
			if judge {
				if !live {
					fmt.Println("Warning: --judge requires --live; ignoring --judge flag")
				} else {
					runOpts = &eval.RunOptions{
						Judge: eval.NewLLMJudge(gw.LLMProvider()),
					}
					fmt.Println("LLM Judge: enabled")
				}
			}

			ctx := context.Background()
			result, err := eval.RunSuiteWithOptions(ctx, runID, tasks, runner, runOpts)
			if err != nil {
				return fmt.Errorf("run suite: %w", err)
			}

			summary := result.Summary()
			fmt.Printf("\n--- Results ---\n")
			fmt.Printf("Total: %d | Passed: %d | Failed: %d | Errors: %d\n",
				summary.TotalTasks, summary.Passed, summary.Failed, summary.Errors)
			fmt.Printf("Success Rate: %.1f%%\n", summary.SuccessRate*100)
			fmt.Printf("Avg Assertion Pass Rate: %.1f%%\n", summary.AvgAssertionPassRate*100)
			fmt.Printf("Avg Confidence: %.2f\n", summary.AvgConfidence)
			fmt.Printf("Avg Replan Count: %.1f\n", summary.AvgReplanCount)
			fmt.Printf("Duration: %.1fs\n", summary.Duration.Seconds())

			if output != "" {
				if err := result.SaveJSON(output); err != nil {
					return fmt.Errorf("save results: %w", err)
				}
				fmt.Printf("\nResults saved to %s\n", output)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&suite, "suite", "builtin", "suite name or path to JSON task file")
	cmd.Flags().StringVarP(&output, "output", "o", "", "output JSON file for results")
	cmd.Flags().StringVar(&runID, "run-id", "", "custom run identifier (auto-generated if empty)")
	cmd.Flags().BoolVar(&live, "live", false, "run against a live cognitive agent (requires LLM credentials)")
	cmd.Flags().StringVarP(&configPath, "config", "c", "configs/ironclaw.yaml", "path to config file (for --live)")
	cmd.Flags().BoolVar(&judge, "judge", false, "enable LLM-as-Judge for tasks with Rubric (requires --live)")
	return cmd
}

// initEvalGateway boots a full gateway for live evaluation. Returns a cleanup
// function that must be called when done.
func initEvalGateway(configPath string) (*gateway.Gateway, func(), error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}

	if err := userdir.Apply(cfg); err != nil {
		slog.Warn("eval: apply user config overlay", "err", err)
	}

	gw, err := gateway.New(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("init gateway: %w", err)
	}

	cleanup := func() {
		if stopErr := gw.Stop(context.Background()); stopErr != nil {
			slog.Warn("eval: gateway stop error", "err", stopErr)
		}
	}

	return gw, cleanup, nil
}

func newEvalCompareCmd() *cobra.Command {
	var (
		beforePath string
		afterPath  string
		jsonOutput bool
	)

	cmd := &cobra.Command{
		Use:   "compare",
		Short: "Compare two evaluation runs",
		RunE: func(cmd *cobra.Command, args []string) error {
			before, err := eval.LoadSuiteResult(beforePath)
			if err != nil {
				return fmt.Errorf("load before: %w", err)
			}

			after, err := eval.LoadSuiteResult(afterPath)
			if err != nil {
				return fmt.Errorf("load after: %w", err)
			}

			report := eval.Compare(before, after)

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}

			fmt.Print(report.FormatMarkdown())
			return nil
		},
	}

	cmd.Flags().StringVar(&beforePath, "before", "", "path to the baseline results JSON")
	cmd.Flags().StringVar(&afterPath, "after", "", "path to the comparison results JSON")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	_ = cmd.MarkFlagRequired("before")
	_ = cmd.MarkFlagRequired("after")
	return cmd
}

func newEvalListCmd() *cobra.Command {
	var suite string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available evaluation tasks",
		Run: func(cmd *cobra.Command, args []string) {
			if suite == "all" {
				for name, fn := range eval.AllSuites() {
					tasks := fn()
					fmt.Printf("Suite: %s (%d tasks)\n", name, len(tasks))
					for _, t := range tasks {
						fmt.Printf("  %-25s [%-8s] %s\n", t.ID, t.Complexity, truncateGoal(t.Goal, 80))
					}
					fmt.Println()
				}
				return
			}
			tasks, err := loadSuite(suite)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				return
			}
			fmt.Printf("Suite %q: %d tasks\n\n", suite, len(tasks))
			for _, t := range tasks {
				fmt.Printf("  %-25s [%-8s] %s\n", t.ID, t.Complexity, truncateGoal(t.Goal, 80))
			}
		},
	}
	cmd.Flags().StringVar(&suite, "suite", "all", "suite to list (builtin, evolution, all)")
	return cmd
}

func newEvalLongitudinalCmd() *cobra.Command {
	var (
		suite         string
		outputDir     string
		iterations    int
		live          bool
		configPath    string
		withWorkload  string
		forceInsights bool
	)

	cmd := &cobra.Command{
		Use:   "longitudinal",
		Short: "Run repeated evaluation cycles to track evolution progress",
		Long: `Run the same eval suite multiple times in sequence. Each iteration's results
are saved to the output directory with an incrementing run ID. After all
iterations, a comparison report and a time-series JSON are generated.

Use --with-workload to inject learning tasks between benchmark iterations.
This generates trajectory data that feeds the evolution engine, enabling
genuine strategy/preference evolution between measurements.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			tasks, err := loadSuite(suite)
			if err != nil {
				return err
			}

			var workloadTasks []eval.TaskCase
			if withWorkload != "" {
				workloadTasks, err = loadSuite(withWorkload)
				if err != nil {
					return fmt.Errorf("load workload suite: %w", err)
				}
			}

			if outputDir == "" {
				outputDir = fmt.Sprintf("eval_longitudinal_%s", time.Now().Format("20060102"))
			}
			if err := os.MkdirAll(outputDir, 0o755); err != nil {
				return fmt.Errorf("create output dir: %w", err)
			}

			var runner eval.AgentRunner
			var gw *gateway.Gateway
			var cleanup func()

			if live {
				var gwErr error
				gw, cleanup, gwErr = initEvalGateway(configPath)
				if gwErr != nil {
					return fmt.Errorf("init live eval: %w", gwErr)
				}
				r := gw.NewEvalRunner()
				if r == nil {
					cleanup()
					return fmt.Errorf("live eval requires agent.mode = cognitive")
				}
				runner = r
			} else {
				runner = &eval.DryRunner{}
			}
			if cleanup != nil {
				defer cleanup()
			}

			ctx := context.Background()
			var results []*eval.SuiteResult
			var points []eval.IterationPoint

			for i := 1; i <= iterations; i++ {
				runID := fmt.Sprintf("iter-%03d", i)
				fmt.Printf("=== Iteration %d/%d (run: %s) ===\n", i, iterations, runID)

				result, runErr := eval.RunSuite(ctx, runID, tasks, runner)
				if runErr != nil {
					return fmt.Errorf("iteration %d: %w", i, runErr)
				}

				outPath := fmt.Sprintf("%s/%s.json", outputDir, runID)
				if err := result.SaveJSON(outPath); err != nil {
					return fmt.Errorf("save iteration %d: %w", i, err)
				}

				summary := result.Summary()
				fmt.Printf("  Success: %.0f%% | Assertions: %.0f%% | Replans: %.1f | Duration: %.1fs\n",
					summary.SuccessRate*100, summary.AvgAssertionPassRate*100,
					summary.AvgReplanCount, summary.Duration.Seconds())

				point := eval.IterationPoint{
					Iteration: i,
					RunID:     runID,
					Timestamp: time.Now(),
					Summary:   summary,
				}

				if sc, ok := runner.(eval.SnapshotCaptor); ok {
					snap := sc.CaptureSnapshot()
					if snap != nil {
						point.StrategyVersion = snap.StrategyVersion
						point.PreferenceCount = snap.PreferenceCount
						point.SkillDraftCount = snap.SkillDraftCount
						point.TrajectoryCount = snap.TrajectoryCount
					}
				}

				points = append(points, point)
				results = append(results, result)

				// Run workload between iterations (skip after last iteration)
				if len(workloadTasks) > 0 && i < iterations {
					fmt.Printf("\n  --- Workload injection (%d tasks) ---\n", len(workloadTasks))
					wlRunID := fmt.Sprintf("workload-%03d", i)
					wlResult, wlErr := eval.RunSuite(ctx, wlRunID, workloadTasks, runner)
					if wlErr != nil {
						slog.Warn("workload iteration failed, continuing", "iter", i, "err", wlErr)
					} else {
						wlSummary := wlResult.Summary()
						fmt.Printf("  Workload: %.0f%% success (%d tasks, %.1fs)\n",
							wlSummary.SuccessRate*100, wlSummary.TotalTasks, wlSummary.Duration.Seconds())
					}

					if forceInsights && gw != nil {
						if evo := gw.EvolutionEngine(); evo != nil {
							evo.WaitPending()
							evo.RunInsightsCycle()
							fmt.Println("  Insights cycle triggered")
						}
					}
					fmt.Println()
				}
			}

			report := eval.NewLongitudinalReport(points)
			reportPath := fmt.Sprintf("%s/longitudinal_report.json", outputDir)
			if err := report.SaveJSON(reportPath); err != nil {
				slog.Warn("failed to write longitudinal report", "err", err)
			} else {
				fmt.Printf("\nLongitudinal report saved to %s\n", reportPath)
			}

			if len(results) >= 2 {
				comparison := eval.Compare(results[0], results[len(results)-1])
				fmt.Println("\n=== Evolution Comparison (first vs last) ===")
				fmt.Print(comparison.FormatMarkdown())

				mdPath := fmt.Sprintf("%s/comparison.md", outputDir)
				if err := os.WriteFile(mdPath, []byte(comparison.FormatMarkdown()), 0o644); err != nil {
					slog.Warn("failed to write comparison report", "err", err)
				} else {
					fmt.Printf("Comparison report saved to %s\n", mdPath)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&suite, "suite", "evolution", "benchmark suite name or JSON file path")
	cmd.Flags().StringVar(&outputDir, "output-dir", "", "directory for iteration results (auto-generated if empty)")
	cmd.Flags().IntVarP(&iterations, "iterations", "n", 3, "number of evaluation iterations")
	cmd.Flags().BoolVar(&live, "live", false, "run against a live cognitive agent")
	cmd.Flags().StringVarP(&configPath, "config", "c", "configs/ironclaw.yaml", "config file path (for --live)")
	cmd.Flags().StringVar(&withWorkload, "with-workload", "", "workload suite to inject between iterations (e.g. 'workload')")
	cmd.Flags().BoolVar(&forceInsights, "force-insights", true, "trigger insights cycle after each workload injection")
	return cmd
}

func newEvalDiagnoseCmd() *cobra.Command {
	var (
		suite      string
		outputDir  string
		live       bool
		judge      bool
		configPath string
		runID      string
	)

	cmd := &cobra.Command{
		Use:   "diagnose",
		Short: "Run evaluation and generate weakness diagnosis report",
		Long: `Runs the full evaluation suite, classifies failures, aggregates dimension scores,
and generates a weakness report with optimization recommendations.

Output includes:
  results.json          — raw evaluation results
  weakness_report.json  — structured weakness report
  weakness_report.md    — readable Markdown report
  radar.html            — radar chart + pie chart + heatmap visualization`,
		RunE: func(cmd *cobra.Command, args []string) error {
			tasks, err := loadSuite(suite)
			if err != nil {
				return err
			}

			if runID == "" {
				runID = fmt.Sprintf("diagnose-%s", time.Now().Format("20060102-150405"))
			}

			if outputDir == "" {
				outputDir = fmt.Sprintf("eval_diagnose_%s", time.Now().Format("20060102"))
			}
			if err := os.MkdirAll(outputDir, 0o755); err != nil {
				return fmt.Errorf("create output dir: %w", err)
			}

			var runner eval.AgentRunner
			var gw *gateway.Gateway

			if live {
				var cleanup func()
				gw, cleanup, err = initEvalGateway(configPath)
				if err != nil {
					return fmt.Errorf("init live eval: %w", err)
				}
				defer cleanup()

				r := gw.NewEvalRunner()
				if r == nil {
					return fmt.Errorf("live eval requires agent.mode = cognitive in config")
				}
				runner = r
				fmt.Printf("Starting LIVE diagnosis run: %s (%d tasks)\n\n", runID, len(tasks))
			} else {
				runner = &eval.DryRunner{}
				fmt.Printf("Starting DRY diagnosis run: %s (%d tasks)\n\n", runID, len(tasks))
			}

			var runOpts *eval.RunOptions
			if judge && live && gw != nil {
				runOpts = &eval.RunOptions{
					Judge: eval.NewLLMJudge(gw.LLMProvider()),
				}
				fmt.Println("LLM Judge: enabled")
			}

			ctx := context.Background()

			// Step 1: Run evaluation
			fmt.Println("=== Step 1: Running evaluation ===")
			suiteResult, err := eval.RunSuiteWithOptions(ctx, runID, tasks, runner, runOpts)
			if err != nil {
				return fmt.Errorf("run suite: %w", err)
			}

			resultsPath := fmt.Sprintf("%s/results.json", outputDir)
			if err := suiteResult.SaveJSON(resultsPath); err != nil {
				slog.Warn("failed to save results", "err", err)
			}

			// Step 2: Diagnose
			fmt.Println("\n=== Step 2: Diagnosing weaknesses ===")
			var provider agent.Provider
			if live && gw != nil {
				provider = gw.LLMProvider()
			}
			classifier := eval.NewFailureClassifier(provider, 5*time.Minute)

			report := eval.Diagnose(ctx, suiteResult, &eval.DiagnoseOptions{
				Classifier: classifier,
				Tasks:      tasks,
			})

			// Step 3: Save reports
			fmt.Println("\n=== Step 3: Generating reports ===")

			jsonPath := fmt.Sprintf("%s/weakness_report.json", outputDir)
			jsonData, err := json.MarshalIndent(report, "", "  ")
			if err == nil {
				if writeErr := os.WriteFile(jsonPath, jsonData, 0o644); writeErr != nil {
					slog.Warn("failed to write JSON report", "err", writeErr)
				} else {
					fmt.Printf("  JSON report: %s\n", jsonPath)
				}
			}

			mdPath := fmt.Sprintf("%s/weakness_report.md", outputDir)
			if writeErr := os.WriteFile(mdPath, []byte(report.FormatMarkdown()), 0o644); writeErr != nil {
				slog.Warn("failed to write Markdown report", "err", writeErr)
			} else {
				fmt.Printf("  Markdown report: %s\n", mdPath)
			}

			radarPath := fmt.Sprintf("%s/radar.html", outputDir)
			if writeErr := writeRadarHTML(report, radarPath); writeErr != nil {
				slog.Warn("failed to write radar chart", "err", writeErr)
			} else {
				fmt.Printf("  Radar chart: %s\n", radarPath)
			}

			// Print summary
			fmt.Printf("\n=== Diagnosis Summary ===\n")
			fmt.Printf("Overall Score: %.2f / 1.00\n", report.OverallScore)
			fmt.Printf("Tasks: %d total, %d failed\n", report.TotalTasks, report.FailedTasks)
			fmt.Printf("Weaknesses found: %d\n", len(report.Weaknesses))
			fmt.Printf("Recommendations: %d\n", len(report.Recommendations))

			if len(report.Weaknesses) > 0 {
				fmt.Println("\nTop weaknesses:")
				for i, w := range report.Weaknesses {
					if i >= 3 {
						break
					}
					fmt.Printf("  [%s] %s: %s\n", strings.ToUpper(w.Severity), w.ID, w.Description)
				}
			}

			fmt.Printf("\nFull report: %s\n", outputDir)
			return nil
		},
	}

	cmd.Flags().StringVar(&suite, "suite", "full", "suite name or JSON file path")
	cmd.Flags().StringVarP(&outputDir, "output", "o", "", "output directory for reports")
	cmd.Flags().BoolVar(&live, "live", false, "run against a live cognitive agent")
	cmd.Flags().BoolVar(&judge, "judge", true, "enable LLM-as-Judge")
	cmd.Flags().StringVarP(&configPath, "config", "c", "configs/ironclaw.yaml", "config file path")
	cmd.Flags().StringVar(&runID, "run-id", "", "custom run identifier")
	return cmd
}

// loadSuite resolves a suite name to task cases. Checks named suites first,
// then falls back to reading a JSON file.
func loadSuite(name string) ([]eval.TaskCase, error) {
	suites := eval.AllSuites()
	if fn, ok := suites[name]; ok {
		return fn(), nil
	}

	data, err := os.ReadFile(name)
	if err != nil {
		available := make([]string, 0, len(suites))
		for k := range suites {
			available = append(available, k)
		}
		return nil, fmt.Errorf("unknown suite %q (available: %v); also not a readable file: %w", name, available, err)
	}
	var tasks []eval.TaskCase
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, fmt.Errorf("parse suite file %s: %w", name, err)
	}
	return tasks, nil
}

func truncateGoal(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
