package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

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
	cmd.AddCommand(newEvalRunCmd(), newEvalCompareCmd(), newEvalListCmd(), newEvalLongitudinalCmd())
	return cmd
}

func newEvalRunCmd() *cobra.Command {
	var (
		suite      string
		output     string
		runID      string
		live       bool
		configPath string
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

			if live {
				gw, cleanup, err := initEvalGateway(configPath)
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

			ctx := context.Background()
			result, err := eval.RunSuite(ctx, runID, tasks, runner)
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
		suite      string
		outputDir  string
		iterations int
		live       bool
		configPath string
	)

	cmd := &cobra.Command{
		Use:   "longitudinal",
		Short: "Run repeated evaluation cycles to track evolution progress",
		Long: `Run the same eval suite multiple times in sequence. Each iteration's results
are saved to the output directory with an incrementing run ID. After all
iterations, a comparison report is generated between the first and last run.

This command is designed for measuring self-evolution effectiveness:
run it periodically as the agent processes real tasks, and compare how
the same benchmark tasks perform over time.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			tasks, err := loadSuite(suite)
			if err != nil {
				return err
			}

			if outputDir == "" {
				outputDir = fmt.Sprintf("eval_longitudinal_%s", time.Now().Format("20060102"))
			}
			if err := os.MkdirAll(outputDir, 0o755); err != nil {
				return fmt.Errorf("create output dir: %w", err)
			}

			var runner eval.AgentRunner
			var cleanup func()

			if live {
				gw, c, err := initEvalGateway(configPath)
				if err != nil {
					return fmt.Errorf("init live eval: %w", err)
				}
				cleanup = c
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

			for i := 1; i <= iterations; i++ {
				runID := fmt.Sprintf("iter-%03d", i)
				fmt.Printf("=== Iteration %d/%d (run: %s) ===\n", i, iterations, runID)

				result, err := eval.RunSuite(ctx, runID, tasks, runner)
				if err != nil {
					return fmt.Errorf("iteration %d: %w", i, err)
				}

				outPath := fmt.Sprintf("%s/%s.json", outputDir, runID)
				if err := result.SaveJSON(outPath); err != nil {
					return fmt.Errorf("save iteration %d: %w", i, err)
				}

				summary := result.Summary()
				fmt.Printf("  Success: %.0f%% | Assertions: %.0f%% | Replans: %.1f | Duration: %.1fs\n\n",
					summary.SuccessRate*100, summary.AvgAssertionPassRate*100,
					summary.AvgReplanCount, summary.Duration.Seconds())

				results = append(results, result)
			}

			if len(results) >= 2 {
				report := eval.Compare(results[0], results[len(results)-1])
				fmt.Println("=== Evolution Comparison (first vs last) ===")
				fmt.Print(report.FormatMarkdown())

				reportPath := fmt.Sprintf("%s/comparison.md", outputDir)
				if err := os.WriteFile(reportPath, []byte(report.FormatMarkdown()), 0o644); err != nil {
					slog.Warn("failed to write comparison report", "err", err)
				} else {
					fmt.Printf("\nComparison report saved to %s\n", reportPath)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&suite, "suite", "evolution", "suite name or JSON file path")
	cmd.Flags().StringVar(&outputDir, "output-dir", "", "directory for iteration results (auto-generated if empty)")
	cmd.Flags().IntVarP(&iterations, "iterations", "n", 3, "number of evaluation iterations")
	cmd.Flags().BoolVar(&live, "live", false, "run against a live cognitive agent")
	cmd.Flags().StringVarP(&configPath, "config", "c", "configs/ironclaw.yaml", "config file path (for --live)")
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
