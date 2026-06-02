package main

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/eval"
	"github.com/spf13/cobra"
)

func newTrainingCmd() *cobra.Command {
	var (
		trajectoryDir string
		outputDir     string
		format        string
		minReward     float64
		minConfidence float64
		sinceDays     int
	)

	cmd := &cobra.Command{
		Use:   "training",
		Short: "Export training data from trajectories",
	}

	exportCmd := &cobra.Command{
		Use:   "export",
		Short: "Export trajectory data in RLHF/DPO/SFT format",
		RunE: func(cmd *cobra.Command, args []string) error {
			if trajectoryDir == "" {
				home, err := ironclawHome()
				if err != nil {
					return err
				}
				trajectoryDir = home + "/evolution/trajectories"
			}
			if outputDir == "" {
				outputDir = "./training_output"
			}

			var tf eval.TrainingFormat
			switch format {
			case "reward", "rlhf":
				tf = eval.FormatReward
			case "dpo":
				tf = eval.FormatDPO
			case "sft":
				tf = eval.FormatSFT
			default:
				return fmt.Errorf("unsupported format: %s (use reward, dpo, or sft)", format)
			}

			since := time.Now().AddDate(0, 0, -sinceDays)

			slog.Info("exporting training data",
				"format", format,
				"trajectory_dir", trajectoryDir,
				"output_dir", outputDir,
				"since", since.Format("2006-01-02"),
			)

			result, err := eval.ExportTrainingData(eval.ExportConfig{
				TrajectoryDir: trajectoryDir,
				OutputDir:     outputDir,
				Format:        tf,
				MinReward:     minReward,
				MinConfidence: minConfidence,
				Since:         since,
			})
			if err != nil {
				return fmt.Errorf("export failed: %w", err)
			}

			fmt.Printf("Export complete:\n")
			fmt.Printf("  Format:  %s\n", result.Format)
			fmt.Printf("  Output:  %s\n", result.OutputPath)
			switch result.Format {
			case eval.FormatReward:
				fmt.Printf("  Samples: %d\n", result.Samples)
			case eval.FormatDPO:
				fmt.Printf("  Pairs:   %d\n", result.Pairs)
			case eval.FormatSFT:
				fmt.Printf("  Samples: %d\n", result.SFTSamples)
			}

			return nil
		},
	}

	exportCmd.Flags().StringVar(&trajectoryDir, "trajectory-dir", "", "trajectory JSONL directory (default: ~/.IronClaw/evolution/trajectories)")
	exportCmd.Flags().StringVarP(&outputDir, "output", "o", "", "output directory (default: ./training_output)")
	exportCmd.Flags().StringVarP(&format, "format", "f", "reward", "export format: reward, dpo, or sft")
	exportCmd.Flags().Float64Var(&minReward, "min-reward", 0, "minimum reward threshold")
	exportCmd.Flags().Float64Var(&minConfidence, "min-confidence", 0, "minimum confidence threshold")
	exportCmd.Flags().IntVar(&sinceDays, "days", 30, "include trajectories from last N days")

	cmd.AddCommand(exportCmd)
	return cmd
}

func ironclawHome() (string, error) {
	home, err := skillsDir()
	if err != nil {
		return "", err
	}
	// skillsDir returns ~/.IronClaw/skills, go up one level
	return home + "/..", nil
}
