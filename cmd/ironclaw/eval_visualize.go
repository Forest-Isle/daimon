package main

import (
	"fmt"
	"html/template"
	"os"

	"github.com/Forest-Isle/IronClaw/internal/eval"
	"github.com/spf13/cobra"
)

func newEvalVisualizeCmd() *cobra.Command {
	var (
		inputPath  string
		outputPath string
	)

	cmd := &cobra.Command{
		Use:   "visualize",
		Short: "Generate an HTML visualization from a longitudinal report",
		Long: `Reads a longitudinal_report.json (produced by 'eval longitudinal') and
generates a self-contained HTML page with interactive Chart.js charts showing
how agent performance evolves over evaluation iterations.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := eval.LoadLongitudinalReport(inputPath)
			if err != nil {
				return fmt.Errorf("load report: %w", err)
			}

			if len(report.Iterations) == 0 {
				return fmt.Errorf("report contains no iterations")
			}

			if outputPath == "" {
				outputPath = "evolution_chart.html"
			}

			f, err := os.Create(outputPath)
			if err != nil {
				return fmt.Errorf("create output: %w", err)
			}
			defer func() { _ = f.Close() }()

			tmpl, err := template.New("chart").Parse(chartTemplate)
			if err != nil {
				return fmt.Errorf("parse template: %w", err)
			}

			data := buildChartData(report)
			if err := tmpl.Execute(f, data); err != nil {
				return fmt.Errorf("render template: %w", err)
			}

			fmt.Printf("Visualization saved to %s\n", outputPath)
			return nil
		},
	}

	cmd.Flags().StringVarP(&inputPath, "input", "i", "longitudinal_report.json", "path to longitudinal report JSON")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "output HTML file (default: evolution_chart.html)")
	return cmd
}

type chartData struct {
	Title            string
	Labels           []int
	SuccessRates     []float64
	AssertPassRates  []float64
	AvgReplans       []float64
	AvgConfidence    []float64
	StrategyVersions []int
	Durations        []float64
	DeltaSummary     string
}

func buildChartData(report *eval.LongitudinalReport) chartData {
	d := chartData{
		Title: "IronClaw Evolution Progress",
	}

	for _, p := range report.Iterations {
		d.Labels = append(d.Labels, p.Iteration)
		d.SuccessRates = append(d.SuccessRates, p.Summary.SuccessRate*100)
		d.AssertPassRates = append(d.AssertPassRates, p.Summary.AvgAssertionPassRate*100)
		d.AvgReplans = append(d.AvgReplans, p.Summary.AvgReplanCount)
		d.AvgConfidence = append(d.AvgConfidence, p.Summary.AvgConfidence)
		d.StrategyVersions = append(d.StrategyVersions, p.StrategyVersion)
		d.Durations = append(d.Durations, p.Summary.Duration.Seconds())
	}

	delta := report.Deltas
	improvement := "no change"
	if delta.SuccessRateDelta > 0 {
		improvement = fmt.Sprintf("+%.1f%% improvement", delta.SuccessRateDelta*100)
	} else if delta.SuccessRateDelta < 0 {
		improvement = fmt.Sprintf("%.1f%% regression", delta.SuccessRateDelta*100)
	}
	d.DeltaSummary = fmt.Sprintf("Success Rate: %s | Confidence: %+.2f | Replans: %+.1f",
		improvement, delta.AvgConfidenceDelta, delta.AvgReplanCountDelta)

	return d
}

const chartTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Title}}</title>
<script src="https://cdn.jsdelivr.net/npm/chart.js@4.4.0/dist/chart.umd.min.js"></script>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: #1a1b2e;
    color: #e0e0e0;
    padding: 2rem;
    min-height: 100vh;
  }
  h1 {
    text-align: center;
    font-size: 1.8rem;
    font-weight: 600;
    margin-bottom: 2rem;
    color: #ffffff;
    letter-spacing: 0.02em;
  }
  .charts-container {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 1.5rem;
    max-width: 1400px;
    margin: 0 auto 2rem;
  }
  .chart-box {
    background: #252640;
    border-radius: 12px;
    padding: 1.5rem;
    border: 1px solid #3a3b5c;
  }
  .summary {
    max-width: 1400px;
    margin: 0 auto;
    background: #252640;
    border-radius: 12px;
    padding: 1.2rem 1.5rem;
    border: 1px solid #3a3b5c;
    text-align: center;
    font-size: 0.95rem;
    color: #b0b0c0;
    letter-spacing: 0.01em;
  }
  .summary strong { color: #ffffff; }
  @media (max-width: 900px) {
    .charts-container { grid-template-columns: 1fr; }
    body { padding: 1rem; }
  }
</style>
</head>
<body>
<h1>{{.Title}}</h1>
<div class="charts-container">
  <div class="chart-box"><canvas id="performanceChart"></canvas></div>
  <div class="chart-box"><canvas id="behaviorChart"></canvas></div>
</div>
<div class="summary"><strong>Deltas (first → last):</strong> {{.DeltaSummary}}</div>
<script>
  const labels = [{{range .Labels}}{{.}},{{end}}];
  const successRates = [{{range .SuccessRates}}{{.}},{{end}}];
  const assertPassRates = [{{range .AssertPassRates}}{{.}},{{end}}];
  const strategyVersions = [{{range .StrategyVersions}}{{.}},{{end}}];
  const avgConfidence = [{{range .AvgConfidence}}{{.}},{{end}}];
  const avgReplans = [{{range .AvgReplans}}{{.}},{{end}}];

  const gridColor = 'rgba(255,255,255,0.08)';
  const tickColor = '#888';

  new Chart(document.getElementById('performanceChart'), {
    type: 'line',
    data: {
      labels: labels,
      datasets: [
        {
          label: 'Success Rate (%)',
          data: successRates,
          borderColor: '#4ade80',
          backgroundColor: 'rgba(74,222,128,0.1)',
          yAxisID: 'y',
          tension: 0.3,
          pointRadius: 4,
          pointHoverRadius: 6,
          fill: true
        },
        {
          label: 'Assertion Pass Rate (%)',
          data: assertPassRates,
          borderColor: '#60a5fa',
          backgroundColor: 'rgba(96,165,250,0.1)',
          yAxisID: 'y',
          tension: 0.3,
          pointRadius: 4,
          pointHoverRadius: 6,
          fill: true
        },
        {
          label: 'Strategy Version',
          data: strategyVersions,
          borderColor: '#fb923c',
          backgroundColor: 'rgba(251,146,60,0.1)',
          yAxisID: 'y1',
          stepped: true,
          pointRadius: 4,
          pointHoverRadius: 6,
          borderDash: [6, 3]
        }
      ]
    },
    options: {
      responsive: true,
      interaction: { mode: 'index', intersect: false },
      plugins: {
        title: { display: true, text: 'Performance & Strategy', color: '#fff', font: { size: 14 } },
        legend: { labels: { color: '#ccc', usePointStyle: true, padding: 16 } }
      },
      scales: {
        x: {
          title: { display: true, text: 'Iteration', color: tickColor },
          ticks: { color: tickColor },
          grid: { color: gridColor }
        },
        y: {
          type: 'linear',
          position: 'left',
          min: 0,
          max: 100,
          title: { display: true, text: 'Percentage (%)', color: tickColor },
          ticks: { color: tickColor },
          grid: { color: gridColor }
        },
        y1: {
          type: 'linear',
          position: 'right',
          title: { display: true, text: 'Strategy Version', color: tickColor },
          ticks: { color: tickColor, stepSize: 1 },
          grid: { drawOnChartArea: false }
        }
      }
    }
  });

  new Chart(document.getElementById('behaviorChart'), {
    type: 'line',
    data: {
      labels: labels,
      datasets: [
        {
          label: 'Avg Confidence',
          data: avgConfidence,
          borderColor: '#c084fc',
          backgroundColor: 'rgba(192,132,252,0.1)',
          yAxisID: 'y',
          tension: 0.3,
          pointRadius: 4,
          pointHoverRadius: 6,
          fill: true
        },
        {
          label: 'Avg Replan Count',
          data: avgReplans,
          borderColor: '#f87171',
          backgroundColor: 'rgba(248,113,113,0.1)',
          yAxisID: 'y1',
          tension: 0.3,
          pointRadius: 4,
          pointHoverRadius: 6,
          fill: true
        }
      ]
    },
    options: {
      responsive: true,
      interaction: { mode: 'index', intersect: false },
      plugins: {
        title: { display: true, text: 'Behavior Metrics', color: '#fff', font: { size: 14 } },
        legend: { labels: { color: '#ccc', usePointStyle: true, padding: 16 } }
      },
      scales: {
        x: {
          title: { display: true, text: 'Iteration', color: tickColor },
          ticks: { color: tickColor },
          grid: { color: gridColor }
        },
        y: {
          type: 'linear',
          position: 'left',
          min: 0,
          max: 1,
          title: { display: true, text: 'Confidence (0–1)', color: tickColor },
          ticks: { color: tickColor },
          grid: { color: gridColor }
        },
        y1: {
          type: 'linear',
          position: 'right',
          min: 0,
          title: { display: true, text: 'Replan Count', color: tickColor },
          ticks: { color: tickColor, stepSize: 1 },
          grid: { drawOnChartArea: false }
        }
      }
    }
  });
</script>
</body>
</html>`
