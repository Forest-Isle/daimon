package cogmetrics

import "time"

// HealthReport is a point-in-time snapshot of cognitive agent health metrics.
type HealthReport struct {
	Timestamp        time.Time     `json:"timestamp"`
	Uptime           time.Duration `json:"uptime_ms"`
	TotalEpisodes    int64         `json:"total_episodes"`
	TotalReflections int64         `json:"total_reflections"`
	StrategyVersion  int           `json:"strategy_version"`

	AssertionPassRate MetricValue            `json:"assertion_pass_rate"`
	ReplanRate        MetricValue            `json:"replan_rate"`
	ReplanEfficiency  ReplanEfficiency       `json:"replan_efficiency"`
	AvgConfidence     MetricValue            `json:"avg_confidence"`
	ToolReliability   map[string]MetricValue `json:"tool_reliability"`
	ComplexitySuccess map[string]MetricValue `json:"complexity_success"`
}

// MetricValue holds a numeric value with sample count for context.
type MetricValue struct {
	Value   float64 `json:"value"`
	Samples int     `json:"samples"`
}

// ReplanEfficiency compares success rates with and without replanning.
type ReplanEfficiency struct {
	WithReplan    MetricValue `json:"with_replan"`
	WithoutReplan MetricValue `json:"without_replan"`
}

// Snapshot captures the current state of all metrics. Thread-safe.
func (c *Collector) Snapshot() HealthReport {
	c.mu.Lock()
	defer c.mu.Unlock()

	report := HealthReport{
		Timestamp:        time.Now(),
		Uptime:           time.Since(c.startedAt),
		TotalEpisodes:    c.totalEpisodes,
		TotalReflections: c.totalReflections,
		StrategyVersion:  c.strategyVersion,
		AssertionPassRate: MetricValue{
			Value:   c.assertionPassRate.Avg(),
			Samples: c.assertionPassRate.Count(),
		},
		ReplanRate: MetricValue{
			Value:   c.replanRate.Avg(),
			Samples: c.replanRate.Count(),
		},
		ReplanEfficiency: ReplanEfficiency{
			WithReplan: MetricValue{
				Value:   c.replanSuccess.Avg(),
				Samples: c.replanSuccess.Count(),
			},
			WithoutReplan: MetricValue{
				Value:   c.noReplanSuccess.Avg(),
				Samples: c.noReplanSuccess.Count(),
			},
		},
		AvgConfidence: MetricValue{
			Value:   c.avgConfidence.Avg(),
			Samples: c.avgConfidence.Count(),
		},
		ToolReliability:   make(map[string]MetricValue, len(c.toolReliability)),
		ComplexitySuccess: make(map[string]MetricValue, len(c.complexitySuccess)),
	}

	for name, ra := range c.toolReliability {
		report.ToolReliability[name] = MetricValue{
			Value:   ra.Avg(),
			Samples: ra.Count(),
		}
	}
	for level, ra := range c.complexitySuccess {
		report.ComplexitySuccess[level] = MetricValue{
			Value:   ra.Avg(),
			Samples: ra.Count(),
		}
	}

	return report
}
