package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/eval"
	"github.com/spf13/cobra"
)

func newEvalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Evaluate agent performance with reproducible task suites",
	}
	cmd.AddCommand(newEvalRunCmd(), newEvalCompareCmd(), newEvalListCmd())
	return cmd
}

func newEvalRunCmd() *cobra.Command {
	var (
		suite  string
		output string
		runID  string
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run an evaluation suite and record results",
		RunE: func(cmd *cobra.Command, args []string) error {
			var tasks []eval.TaskCase

			switch suite {
			case "builtin":
				tasks = eval.BuiltinSuite()
			default:
				data, err := os.ReadFile(suite)
				if err != nil {
					return fmt.Errorf("read suite file: %w", err)
				}
				if err := json.Unmarshal(data, &tasks); err != nil {
					return fmt.Errorf("parse suite file: %w", err)
				}
			}

			if runID == "" {
				runID = fmt.Sprintf("eval-%s", time.Now().Format("20060102-150405"))
			}

			fmt.Printf("Starting evaluation run: %s (%d tasks)\n\n", runID, len(tasks))

			// Use a DryRunner that records task structure without calling a real LLM.
			// For live evaluation, the gateway would wire a real AgentRunner.
			runner := &eval.DryRunner{}
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
	return cmd
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
	return &cobra.Command{
		Use:   "list",
		Short: "List available evaluation tasks in the builtin suite",
		Run: func(cmd *cobra.Command, args []string) {
			tasks := eval.BuiltinSuite()
			fmt.Printf("Builtin suite: %d tasks\n\n", len(tasks))
			for _, t := range tasks {
				fmt.Printf("  %-20s [%s] %s\n", t.ID, t.Complexity, t.Goal)
			}
		},
	}
}
