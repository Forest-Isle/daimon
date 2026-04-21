package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
	cmd.AddCommand(newEvalRunCmd(), newEvalCompareCmd(), newEvalListCmd(), newEvalLongitudinalCmd(), newEvalVisualizeCmd(), newEvalDiagnoseCmd(), newEvalAdaptiveCmd(), newEvalBenchmarkCmd())
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
//
// The config is adjusted for eval mode:
//   - agent.mode forced to "cognitive"
//   - evolution.enabled forced to true (so StrategyOptimizer/PreferenceLearner
//     can learn from eval feedback and evo_before/evo_after are populated)
//   - permissions.default set to "none" and deny rules cleared (eval measures
//     agent capability, not permission policy; tool denials distort results)
//   - tool requires_approval set to false (EvalChannel auto-approves, but
//     bypassing the approval flow entirely is more reliable)
func initEvalGateway(configPath string) (*gateway.Gateway, func(), error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}

	if err := userdir.Apply(cfg); err != nil {
		slog.Warn("eval: apply user config overlay", "err", err)
	}

	// Force cognitive mode for eval
	cfg.Agent.Mode = "cognitive"

	// Enable evolution engine so hooks capture reflection/episode/tool metrics
	cfg.Evolution.Enabled = true

	// Remove all permission barriers for eval — we want to measure agent
	// capability, not security policy enforcement
	cfg.Permissions.Default = "none"
	cfg.Permissions.Rules = nil
	cfg.Tools.Bash.RequiresApproval = false
	cfg.Tools.File.RequiresApproval = false
	cfg.Tools.HTTP.RequiresApproval = false
	cfg.Tools.Browser.RequiresApproval = false

	slog.Info("eval: config overrides applied",
		"agent.mode", "cognitive",
		"evolution.enabled", true,
		"permissions.default", "none",
	)

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
		beforePath       string
		afterPath        string
		jsonOutput       bool
		failOnRegression bool
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
				if encErr := enc.Encode(report); encErr != nil {
					return encErr
				}
			} else {
				fmt.Print(report.FormatMarkdown())
			}

			if failOnRegression && len(report.Regressions) > 0 {
				fmt.Fprintf(os.Stderr, "❌ %d regression(s) detected.\n", len(report.Regressions))
				os.Exit(1)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&beforePath, "before", "", "path to the baseline results JSON")
	cmd.Flags().StringVar(&afterPath, "after", "", "path to the comparison results JSON")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	cmd.Flags().BoolVar(&failOnRegression, "fail-on-regression", false, "exit with code 1 if any regressions are detected")
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
		judge         bool
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

			var runOpts *eval.RunOptions
			if judge && live && gw != nil {
				runOpts = &eval.RunOptions{
					Judge: eval.NewLLMJudge(gw.LLMProvider()),
				}
				fmt.Println("LLM Judge: enabled")
			}

			ctx := context.Background()
			var results []*eval.SuiteResult
			var points []eval.IterationPoint

			for i := 1; i <= iterations; i++ {
				runID := fmt.Sprintf("iter-%03d", i)
				fmt.Printf("=== Iteration %d/%d (run: %s) ===\n", i, iterations, runID)

				result, runErr := eval.RunSuiteWithOptions(ctx, runID, tasks, runner, runOpts)
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

				if len(workloadTasks) > 0 && i < iterations {
					fmt.Printf("\n  --- Workload injection (%d tasks) ---\n", len(workloadTasks))
					wlRunID := fmt.Sprintf("workload-%03d", i)
					wlResult, wlErr := eval.RunSuiteWithOptions(ctx, wlRunID, workloadTasks, runner, runOpts)
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
	cmd.Flags().BoolVar(&judge, "judge", true, "enable LLM-as-Judge for tasks with Rubric (requires --live)")
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

func newEvalAdaptiveCmd() *cobra.Command {
	var (
		suite         string
		outputDir     string
		rounds        int
		tasksPerRound int
		live          bool
		configPath    string
	)

	cmd := &cobra.Command{
		Use:   "adaptive",
		Short: "Run multi-round adaptive evaluation targeting weaknesses",
		Long: `Runs multiple evaluation rounds. After each round, the system diagnoses
weaknesses and generates targeted tasks for the next round. This creates
a feedback loop that progressively challenges the agent's weak areas.

Output includes per-round reports and an overall adaptive summary with
convergence/divergence trend analysis.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			tasks, err := loadSuite(suite)
			if err != nil {
				return err
			}

			if outputDir == "" {
				outputDir = fmt.Sprintf("eval_adaptive_%s", time.Now().Format("20060102"))
			}
			if err := os.MkdirAll(outputDir, 0o755); err != nil {
				return fmt.Errorf("create output dir: %w", err)
			}

			var runner eval.AgentRunner
			var gw *gateway.Gateway
			var provider agent.Provider

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
				provider = gw.LLMProvider()
			} else {
				runner = &eval.DryRunner{}
				fmt.Println("Warning: adaptive mode without --live uses DryRunner (no real LLM)")
			}

			fmt.Printf("Starting adaptive evaluation: %d rounds, %d tasks/round, base suite: %s (%d tasks)\n\n",
				rounds, tasksPerRound, suite, len(tasks))

			ctx := context.Background()
			summary, err := eval.RunAdaptiveLoop(ctx, tasks, eval.AdaptiveLoopOptions{
				Suite:         suite,
				Rounds:        rounds,
				TasksPerRound: tasksPerRound,
				Runner:        runner,
				Provider:      provider,
				OutputDir:     outputDir,
			})
			if err != nil {
				return fmt.Errorf("adaptive loop: %w", err)
			}

			summaryJSON, _ := json.MarshalIndent(summary, "", "  ")
			_ = os.WriteFile(outputDir+"/adaptive_summary.json", summaryJSON, 0o644)
			_ = os.WriteFile(outputDir+"/adaptive_summary.md", []byte(summary.FormatMarkdown()), 0o644)

			if err := writeAdaptiveTrendHTML(summary, outputDir+"/trend.html"); err != nil {
				slog.Warn("failed to write trend chart", "err", err)
			}

			fmt.Printf("\n=== Adaptive Evaluation Complete ===\n")
			fmt.Printf("Rounds: %d\n", len(summary.Rounds))
			if len(summary.Converging) > 0 {
				fmt.Printf("Improving: %s\n", strings.Join(summary.Converging, ", "))
			}
			if len(summary.Diverging) > 0 {
				fmt.Printf("Declining: %s\n", strings.Join(summary.Diverging, ", "))
			}
			fmt.Printf("\nReports saved to %s/\n", outputDir)

			return nil
		},
	}

	cmd.Flags().StringVar(&suite, "suite", "full", "base suite name")
	cmd.Flags().StringVarP(&outputDir, "output", "o", "", "output directory")
	cmd.Flags().IntVarP(&rounds, "rounds", "n", 3, "number of adaptive rounds")
	cmd.Flags().IntVar(&tasksPerRound, "tasks-per-round", 6, "number of tasks to generate per round")
	cmd.Flags().BoolVar(&live, "live", false, "run against a live cognitive agent")
	cmd.Flags().StringVarP(&configPath, "config", "c", "configs/ironclaw.yaml", "config file path")
	return cmd
}

func newEvalBenchmarkCmd() *cobra.Command {
	var (
		benchmarkName string
		dataPath      string
		outputDir     string
		live          bool
		judge         bool
		configPath    string
	)

	cmd := &cobra.Command{
		Use:   "benchmark",
		Short: "Run external benchmark (swe-bench, humaneval, gaia)",
		Long: `Load and run tasks from an external benchmark dataset.

Supported benchmarks:
  swe-bench  — Software engineering bug-fix tasks
  humaneval  — Python function implementation tasks
  gaia       — Real-world multi-step reasoning tasks

Each benchmark requires a dataset file (--data). Results are compared
against known reference scores from other agents.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			adapters := eval.AllBenchmarkAdapters()
			adapter, ok := adapters[benchmarkName]
			if !ok {
				names := make([]string, 0, len(adapters))
				for k := range adapters {
					names = append(names, k)
				}
				return fmt.Errorf("unknown benchmark %q (available: %v)", benchmarkName, names)
			}

			tasks, err := adapter.LoadTasks(dataPath)
			if err != nil {
				return fmt.Errorf("load benchmark tasks: %w", err)
			}

			if outputDir == "" {
				outputDir = fmt.Sprintf("eval_benchmark_%s_%s", benchmarkName, time.Now().Format("20060102"))
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
			} else {
				runner = &eval.DryRunner{}
			}

			runID := fmt.Sprintf("bench-%s-%s", benchmarkName, time.Now().Format("20060102-150405"))
			fmt.Printf("Running benchmark %s: %d tasks (run: %s)\n\n", benchmarkName, len(tasks), runID)

			var runOpts *eval.RunOptions
			if judge && live && gw != nil {
				runOpts = &eval.RunOptions{
					Judge: eval.NewLLMJudge(gw.LLMProvider()),
				}
			}

			ctx := context.Background()
			suiteResult, err := eval.RunSuiteWithOptions(ctx, runID, tasks, runner, runOpts)
			if err != nil {
				return fmt.Errorf("run benchmark: %w", err)
			}

			_ = suiteResult.SaveJSON(outputDir + "/results.json")

			formatted, _ := adapter.FormatResult(suiteResult.Results)
			_ = os.WriteFile(outputDir+"/benchmark_results.json", formatted, 0o644)

			var refs []eval.ReferenceScore
			switch benchmarkName {
			case "swe-bench":
				refs = eval.SWEBenchReferences()
			case "humaneval":
				refs = eval.HumanEvalReferences()
			case "gaia":
				refs = eval.GAIAReferences()
			}

			comparison := eval.ComputeBenchmarkComparison(benchmarkName, suiteResult.Results, refs)
			_ = comparison.SaveJSON(outputDir + "/comparison.json")
			_ = os.WriteFile(outputDir+"/comparison.md", []byte(comparison.FormatComparisonMarkdown()), 0o644)

			fmt.Printf("\n=== Benchmark Results: %s ===\n", benchmarkName)
			fmt.Printf("IronClaw Score: %.1f%% (%d/%d passed)\n", comparison.IronClawScore*100, comparison.PassedTasks, comparison.TotalTasks)

			if len(refs) > 0 {
				fmt.Println("\nReference Scores:")
				for _, ref := range refs {
					fmt.Printf("  %s: %.1f%% (%s)\n", ref.AgentName, ref.Score*100, ref.Source)
				}
			}

			fmt.Printf("\nReports saved to %s/\n", outputDir)
			return nil
		},
	}

	cmd.Flags().StringVar(&benchmarkName, "name", "", "benchmark name (swe-bench, humaneval, gaia)")
	cmd.Flags().StringVar(&dataPath, "data", "", "path to benchmark dataset JSON file")
	cmd.Flags().StringVarP(&outputDir, "output", "o", "", "output directory")
	cmd.Flags().BoolVar(&live, "live", false, "run against a live cognitive agent")
	cmd.Flags().BoolVar(&judge, "judge", true, "enable LLM-as-Judge")
	cmd.Flags().StringVarP(&configPath, "config", "c", "configs/ironclaw.yaml", "config file path")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("data")
	return cmd
}

// loadSuite resolves a suite name to task cases. Checks named suites first,
// then falls back to reading a file (YAML for .yaml/.yml, JSON otherwise).
func loadSuite(name string) ([]eval.TaskCase, error) {
	suites := eval.AllSuites()
	if fn, ok := suites[name]; ok {
		return fn(), nil
	}

	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml":
		tasks, err := eval.LoadTaskSetYAML(name)
		if err != nil {
			available := make([]string, 0, len(suites))
			for k := range suites {
				available = append(available, k)
			}
			return nil, fmt.Errorf("unknown suite %q (available: %v); %w", name, available, err)
		}
		return tasks, nil
	default:
		tasks, err := eval.LoadTaskSetJSON(name)
		if err != nil {
			available := make([]string, 0, len(suites))
			for k := range suites {
				available = append(available, k)
			}
			return nil, fmt.Errorf("unknown suite %q (available: %v); %w", name, available, err)
		}
		return tasks, nil
	}
}

func truncateGoal(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
