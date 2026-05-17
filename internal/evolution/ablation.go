package evolution

import (
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"
)

// AblationEngine automatically tests "what happens when I disable X?"
type AblationEngine struct {
	components map[string]*AblatableComponent
	baseline   *AblationResult
	mu         sync.RWMutex
}

// AblatableComponent describes a feature that can be ablated.
type AblatableComponent struct {
	Name        string
	Description string
	Enabled     bool
	CanDisable  bool
	Importance  float64 // 0-1, how critical this component is
}

// AblationResult holds the outcome of an ablation test.
type AblationResult struct {
	ComponentName   string
	SuccessRate     float64
	AvgConfidence   float64
	AvgDurationMs   int64
	SampleCount     int
	DeltaSuccess    float64 // vs baseline
	DeltaConfidence float64 // vs baseline
	Significant     bool    // |delta| > 5%
	TestedAt        time.Time
}

// NewAblationEngine creates a new ablation engine.
func NewAblationEngine() *AblationEngine {
	return &AblationEngine{
		components: make(map[string]*AblatableComponent),
	}
}

// Register adds a component that can be ablated.
func (ae *AblationEngine) Register(name, description string, canDisable bool, importance float64) {
	ae.mu.Lock()
	defer ae.mu.Unlock()
	ae.components[name] = &AblatableComponent{
		Name:        name,
		Description: description,
		Enabled:     true,
		CanDisable:  canDisable,
		Importance:  importance,
	}
}

// SetEnabled toggles a component's enabled state.
func (ae *AblationEngine) SetEnabled(name string, enabled bool) {
	ae.mu.Lock()
	defer ae.mu.Unlock()
	if c, ok := ae.components[name]; ok {
		c.Enabled = enabled
	}
}

// SetBaseline records the baseline performance with all components enabled.
func (ae *AblationEngine) SetBaseline(result *AblationResult) {
	ae.mu.Lock()
	defer ae.mu.Unlock()
	ae.baseline = result
}

// RunAblationStudy evaluates each ablatable component against baseline.
// evaluator is called with a list of disabled component names and returns metrics.
func (ae *AblationEngine) RunAblationStudy(
	evaluator func(disabled []string) (*AblationResult, error),
) (*AblationReport, error) {
	ae.mu.RLock()
	baseline := ae.baseline
	components := make([]*AblatableComponent, 0, len(ae.components))
	for _, c := range ae.components {
		if c.CanDisable && c.Enabled {
			components = append(components, c)
		}
	}
	ae.mu.RUnlock()

	if baseline == nil {
		return nil, fmt.Errorf("ablation: no baseline set")
	}

	// Sort by importance (test low-importance first — they're less disruptive)
	sort.Slice(components, func(i, j int) bool {
		return components[i].Importance < components[j].Importance
	})

	report := &AblationReport{
		Baseline:  baseline,
		Results:   make(map[string]*AblationResult),
		StartedAt: time.Now(),
	}

	for _, comp := range components {
		result, err := evaluator([]string{comp.Name})
		if err != nil {
			slog.Warn("ablation: component test failed", "component", comp.Name, "err", err)
			continue
		}

		result.ComponentName = comp.Name
		result.DeltaSuccess = result.SuccessRate - baseline.SuccessRate
		result.DeltaConfidence = result.AvgConfidence - baseline.AvgConfidence
		result.Significant = math.Abs(result.DeltaSuccess) > 0.05
		result.TestedAt = time.Now()

		report.Results[comp.Name] = result

		// Alert on negative contribution
		if result.DeltaSuccess > 0.05 {
			slog.Warn("ablation: component has negative contribution! disabling improves performance",
				"component", comp.Name,
				"delta_success", fmt.Sprintf("%+.1f%%", result.DeltaSuccess*100),
				"recommendation", "consider disabling or reworking this component",
			)
			report.NegativeContributors = append(report.NegativeContributors, comp.Name)
		}

		if result.DeltaSuccess < -0.10 {
			slog.Info("ablation: component is critical — strong positive contribution",
				"component", comp.Name,
				"delta_success", fmt.Sprintf("%+.1f%%", result.DeltaSuccess*100),
			)
			report.CriticalComponents = append(report.CriticalComponents, comp.Name)
		}
	}

	report.CompletedAt = time.Now()
	report.Duration = report.CompletedAt.Sub(report.StartedAt)

	return report, nil
}

// AblationReport summarizes the findings of an ablation study.
type AblationReport struct {
	Baseline             *AblationResult
	Results              map[string]*AblationResult
	NegativeContributors []string // disabling these IMPROVES performance
	CriticalComponents   []string // disabling these SEVERELY DEGRADES performance
	StartedAt            time.Time
	CompletedAt          time.Time
	Duration             time.Duration
}

// FormatMarkdown returns a human-readable summary.
func (ar *AblationReport) FormatMarkdown() string {
	if ar == nil {
		return "No ablation data."
	}

	s := fmt.Sprintf(`## Ablation Study Report

**Baseline**: Success=%.1f%%, Confidence=%.2f, Samples=%d

| Component | Δ Success | Δ Confidence | Significant |
|-----------|-----------|-------------|-------------|
`, ar.Baseline.SuccessRate*100, ar.Baseline.AvgConfidence, ar.Baseline.SampleCount)

	for name, r := range ar.Results {
		sig := "no"
		if r.Significant {
			sig = "YES"
		}
		s += fmt.Sprintf("| %s | %+.1f%% | %+.2f | %s |\n",
			name, r.DeltaSuccess*100, r.DeltaConfidence, sig)
	}

	if len(ar.NegativeContributors) > 0 {
		s += "\n### ⚠️ Negative Contributors (disabling improves performance)\n"
		for _, name := range ar.NegativeContributors {
			r := ar.Results[name]
			s += fmt.Sprintf("- **%s**: +%.1f%% success when disabled\n", name, r.DeltaSuccess*100)
		}
	}

	if len(ar.CriticalComponents) > 0 {
		s += "\n### 🔑 Critical Components (do not disable)\n"
		for _, name := range ar.CriticalComponents {
			r := ar.Results[name]
			s += fmt.Sprintf("- **%s**: %.1f%% success drop when disabled\n", name, -r.DeltaSuccess*100)
		}
	}

	return s
}
