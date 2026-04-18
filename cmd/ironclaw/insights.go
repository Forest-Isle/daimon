package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/cogmetrics"
	"github.com/Forest-Isle/IronClaw/internal/evolution"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newInsightsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "insights",
		Short: "Analyze self-evolution trajectory data",
	}
	cmd.AddCommand(newInsightsReportCmd(), newInsightsExportCmd(), newInsightsHealthCmd())
	return cmd
}

func newInsightsReportCmd() *cobra.Command {
	var days int
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "report",
		Short: "Generate an insights report from recent trajectories",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := trajectoriesDir()
			if err != nil {
				return err
			}

			until := time.Now()
			since := until.AddDate(0, 0, -days)

			records, err := evolution.ReadTrajectories(dir, since, until)
			if err != nil {
				return fmt.Errorf("read trajectories: %w", err)
			}

			if len(records) == 0 {
				fmt.Printf("No trajectory data found in the last %d days.\n", days)
				fmt.Println("Enable evolution (evolution.enabled: true) and use IronClaw in cognitive mode to generate data.")
				return nil
			}

			label := fmt.Sprintf("Last %d days (%s to %s)",
				days, since.Format("2006-01-02"), until.Format("2006-01-02"))
			report := evolution.GenerateInsights(records, label)

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}

			fmt.Print(report.FormatMarkdown())
			return nil
		},
	}

	cmd.Flags().IntVar(&days, "days", 7, "number of days to analyze")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON instead of Markdown")
	return cmd
}

func newInsightsExportCmd() *cobra.Command {
	var days int
	var output string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export trajectory data as JSONL for external analysis or RL training",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := trajectoriesDir()
			if err != nil {
				return err
			}

			until := time.Now()
			since := until.AddDate(0, 0, -days)

			records, err := evolution.ReadTrajectories(dir, since, until)
			if err != nil {
				return fmt.Errorf("read trajectories: %w", err)
			}

			if len(records) == 0 {
				fmt.Printf("No trajectory data found in the last %d days.\n", days)
				return nil
			}

			var w *os.File
			if output == "" || output == "-" {
				w = os.Stdout
			} else {
				f, err := os.Create(output)
				if err != nil {
					return fmt.Errorf("create output file: %w", err)
				}
				defer func() { _ = f.Close() }()
				w = f
			}

			enc := json.NewEncoder(w)
			for _, rec := range records {
				if err := enc.Encode(rec); err != nil {
					return fmt.Errorf("encode record: %w", err)
				}
			}

			if output != "" && output != "-" {
				fmt.Fprintf(os.Stderr, "Exported %d records to %s\n", len(records), output)
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&days, "days", 30, "number of days to export")
	cmd.Flags().StringVarP(&output, "output", "o", "-", "output file (- for stdout)")
	return cmd
}

func newInsightsHealthCmd() *cobra.Command {
	var (
		days       int
		jsonOutput bool
	)

	cmd := &cobra.Command{
		Use:   "health",
		Short: "Display cognitive health metrics from trajectory data",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := trajectoriesDir()
			if err != nil {
				return err
			}

			until := time.Now()
			since := until.AddDate(0, 0, -days)

			records, err := evolution.ReadTrajectories(dir, since, until)
			if err != nil {
				return fmt.Errorf("read trajectories: %w", err)
			}

			if len(records) == 0 {
				fmt.Printf("No trajectory data found in the last %d days.\n", days)
				fmt.Println("Enable evolution (evolution.enabled: true) and use IronClaw in cognitive mode to generate data.")
				return nil
			}

			report := buildHealthFromTrajectories(records)

			if jsonOutput {
				js, err := report.FormatJSON()
				if err != nil {
					return err
				}
				fmt.Println(js)
				return nil
			}

			fmt.Print(report.FormatMarkdown())
			return nil
		},
	}

	cmd.Flags().IntVar(&days, "days", 7, "number of days to analyze")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	return cmd
}

func buildHealthFromTrajectories(records []evolution.TrajectoryRecord) *cogmetrics.HealthReport {
	c := cogmetrics.NewCollector()
	ctx := context.Background()

	for _, rec := range records {
		c.OnReflectionComplete(ctx, evolution.ReflectionEvent{
			Complexity: rec.Complexity,
			Succeeded:  rec.Reflection.Succeeded,
			Confidence: rec.Reflection.Confidence,
		})

		c.OnEpisodeComplete(ctx, evolution.EpisodeEvent{
			Succeeded:   rec.Reflection.Succeeded,
			ReplanCount: rec.ReplanCount,
		})

		for _, tool := range rec.Tools {
			c.OnToolExecuted(ctx, evolution.ToolExecEvent{
				ToolName:  tool.Name,
				Succeeded: tool.Succeeded,
			})
		}

		if len(rec.Tools) > 0 {
			passed := 0
			for _, tool := range rec.Tools {
				if tool.Succeeded {
					passed++
				}
			}
			c.RecordAssertionRate(float64(passed) / float64(len(rec.Tools)))
		}
	}

	if v := loadStrategyVersion(); v > 0 {
		c.SetStrategyVersion(v)
	}

	snapshot := c.Snapshot()
	return &snapshot
}

func loadStrategyVersion() int {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0
	}
	data, err := os.ReadFile(filepath.Join(home, ".IronClaw", "evolution", "strategy.yaml"))
	if err != nil {
		return 0
	}
	var s evolution.Strategy
	if err := yaml.Unmarshal(data, &s); err != nil {
		return 0
	}
	return s.Version
}

func trajectoriesDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".IronClaw", "evolution", "trajectories"), nil
}
