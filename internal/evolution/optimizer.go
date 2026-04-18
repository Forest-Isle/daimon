package evolution

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Data types
// ---------------------------------------------------------------------------

// episodeRecord stores the subset of episode data needed for optimization.
type episodeRecord struct {
	Complexity  string
	Succeeded   bool
	TotalReward float64
	ReplanCount int
	ToolsUsed   []string
	Timestamp   time.Time
}

// Strategy holds the current cognitive strategy configuration that the
// optimizer tunes over time. It is persisted to YAML between sessions.
type Strategy struct {
	Version         int                      `yaml:"version"`
	UpdatedAt       time.Time                `yaml:"updated_at"`
	ReplanThreshold StrategyParam            `yaml:"replan_threshold"`
	ToolPriorities  map[string]StrategyParam `yaml:"tool_priorities"`
	Metrics         MetricsSnapshot          `yaml:"metrics"`
}

// StrategyParam is a single tunable parameter with lineage tracking so that
// the optimizer can explain and revert changes.
type StrategyParam struct {
	Value    float64 `yaml:"value"`
	Previous float64 `yaml:"previous"`
	Reason   string  `yaml:"reason"`
}

// MetricsSnapshot records high-level statistics at the time of the last
// optimization cycle.
type MetricsSnapshot struct {
	OverallSuccessRate float64 `yaml:"overall_success_rate"`
	EpisodesAnalyzed   int     `yaml:"episodes_analyzed"`
}

// ---------------------------------------------------------------------------
// StrategyOptimizer — Hook implementation (Loop 3)
// ---------------------------------------------------------------------------

const (
	maxEpisodeWindow       = 100
	defaultReplanThreshold = 0.3
	defaultToolPriority    = 0.5

	// Hard bounds for tunable parameters.
	minReplanThreshold = 0.01
	maxReplanThreshold = 0.99
	minToolPriority    = 0.0
	maxToolPriority    = 1.0

	// Effectiveness thresholds that drive adjustment direction.
	replanEffectiveThreshold   = 0.70
	replanIneffectiveThreshold = 0.30
	toolBoostThreshold         = 0.80
	toolReduceThreshold        = 0.50

	// Minimum observations before a per-tool stat is considered reliable.
	minToolObservations = 3
)

// StrategyOptimizer implements Hook. It collects episode outcomes in a rolling
// window and periodically re-tunes cognitive parameters (replan threshold,
// tool priorities) based on observed success rates.
type StrategyOptimizer struct {
	cfg               OptimizerConfig
	episodes          []episodeRecord
	strategy          *Strategy
	episodeCount      int
	lastOptimizeCount int
	mu                sync.Mutex
}

// compile-time check
var _ Hook = (*StrategyOptimizer)(nil)

// NewStrategyOptimizer creates an optimizer with sensible initial defaults.
func NewStrategyOptimizer(cfg OptimizerConfig) *StrategyOptimizer {
	return &StrategyOptimizer{
		cfg:      cfg,
		episodes: make([]episodeRecord, 0, maxEpisodeWindow),
		strategy: &Strategy{
			Version: 1,
			ReplanThreshold: StrategyParam{
				Value:  defaultReplanThreshold,
				Reason: "initial default",
			},
			ToolPriorities: make(map[string]StrategyParam),
		},
	}
}

// Name implements Hook.
func (so *StrategyOptimizer) Name() string { return "strategy_optimizer" }

// OnReflectionComplete implements Hook (no-op for the optimizer).
func (so *StrategyOptimizer) OnReflectionComplete(_ context.Context, _ ReflectionEvent) {}

// OnToolExecuted implements Hook (no-op — tool stats are aggregated at episode level).
func (so *StrategyOptimizer) OnToolExecuted(_ context.Context, _ ToolExecEvent) {}

// OnEpisodeComplete implements Hook. It records the episode and, every
// UpdateInterval episodes, triggers an optimization cycle.
func (so *StrategyOptimizer) OnEpisodeComplete(_ context.Context, event EpisodeEvent) {
	so.mu.Lock()
	defer so.mu.Unlock()

	// Defensive copy of the tool sequence.
	tools := make([]string, len(event.ToolSequence))
	copy(tools, event.ToolSequence)

	so.episodes = append(so.episodes, episodeRecord{
		Complexity:  event.Complexity,
		Succeeded:   event.Succeeded,
		TotalReward: event.TotalReward,
		ReplanCount: event.ReplanCount,
		ToolsUsed:   tools,
		Timestamp:   event.Timestamp,
	})

	// Enforce rolling window cap — drop oldest entries.
	if len(so.episodes) > maxEpisodeWindow {
		so.episodes = so.episodes[len(so.episodes)-maxEpisodeWindow:]
	}

	so.episodeCount++

	if so.cfg.UpdateInterval > 0 && so.episodeCount%so.cfg.UpdateInterval == 0 {
		so.optimizeLocked()
	}
}

// ---------------------------------------------------------------------------
// Core optimisation logic (caller must hold mu)
// ---------------------------------------------------------------------------

func (so *StrategyOptimizer) optimizeLocked() {
	if len(so.episodes) == 0 {
		return
	}

	slog.Info("strategy_optimizer: starting optimization",
		"episodes", len(so.episodes),
		"cycle", so.episodeCount,
	)

	previousSuccessRate := so.strategy.Metrics.OverallSuccessRate

	stats := so.computeStats()

	so.adjustReplanThreshold(stats)
	so.adjustToolPriorities(stats)

	// Update the metrics snapshot.
	so.strategy.Metrics = MetricsSnapshot{
		OverallSuccessRate: stats.overallSuccessRate,
		EpisodesAnalyzed:   len(so.episodes),
	}
	so.strategy.UpdatedAt = time.Now()
	so.strategy.Version++
	so.lastOptimizeCount = so.episodeCount

	// Revert check: if success rate dropped more than RevertThreshold from the
	// previous cycle's snapshot, roll back parameter changes.
	if previousSuccessRate > 0 {
		decline := previousSuccessRate - stats.overallSuccessRate
		if decline > so.cfg.RevertThreshold {
			slog.Warn("strategy_optimizer: reverting — success rate declined",
				"decline", decline,
				"previous", previousSuccessRate,
				"current", stats.overallSuccessRate,
			)
			so.revertLocked(previousSuccessRate)
			return
		}
	}

	slog.Info("strategy_optimizer: optimization complete",
		"replan_threshold", so.strategy.ReplanThreshold.Value,
		"success_rate", stats.overallSuccessRate,
		"version", so.strategy.Version,
	)

	// Persist strategy (best-effort).
	if so.cfg.StrategyFile != "" {
		if err := so.saveStrategyLocked(so.cfg.StrategyFile); err != nil {
			slog.Error("strategy_optimizer: failed to save strategy", "error", err)
		}
	}
}

// ---------------------------------------------------------------------------
// Statistics
// ---------------------------------------------------------------------------

type optimizeStats struct {
	overallSuccessRate  float64
	replanEffectiveness float64 // success rate of episodes that used replans
	noReplanSuccessRate float64 // success rate of episodes without replans
	withReplanTotal     int
	noReplanTotal       int
	toolSuccess         map[string]int // tool → successes
	toolTotal           map[string]int // tool → total uses
}

func (so *StrategyOptimizer) computeStats() optimizeStats {
	var s optimizeStats
	s.toolSuccess = make(map[string]int)
	s.toolTotal = make(map[string]int)

	var (
		successCount       int
		withReplanSuccess  int
		noReplanSuccess    int
	)

	for _, ep := range so.episodes {
		if ep.Succeeded {
			successCount++
		}

		if ep.ReplanCount > 0 {
			s.withReplanTotal++
			if ep.Succeeded {
				withReplanSuccess++
			}
		} else {
			s.noReplanTotal++
			if ep.Succeeded {
				noReplanSuccess++
			}
		}

		for _, tool := range ep.ToolsUsed {
			s.toolTotal[tool]++
			if ep.Succeeded {
				s.toolSuccess[tool]++
			}
		}
	}

	s.overallSuccessRate = float64(successCount) / float64(len(so.episodes))
	if s.withReplanTotal > 0 {
		s.replanEffectiveness = float64(withReplanSuccess) / float64(s.withReplanTotal)
	}
	if s.noReplanTotal > 0 {
		s.noReplanSuccessRate = float64(noReplanSuccess) / float64(s.noReplanTotal)
	}

	return s
}

// ---------------------------------------------------------------------------
// Parameter adjustments
// ---------------------------------------------------------------------------

func (so *StrategyOptimizer) adjustReplanThreshold(s optimizeStats) {
	if s.withReplanTotal == 0 || s.noReplanTotal == 0 {
		return // insufficient data to compare
	}

	prev := so.strategy.ReplanThreshold.Value
	adjFraction := so.cfg.MaxAdjustmentPercent / 100.0

	switch {
	case s.replanEffectiveness > replanEffectiveThreshold:
		// Replans are effective → lower threshold to trigger more replans.
		newVal := clamp(prev*(1-adjFraction), minReplanThreshold, maxReplanThreshold)
		so.strategy.ReplanThreshold = StrategyParam{
			Value:    newVal,
			Previous: prev,
			Reason: fmt.Sprintf("replan effective (%.1f%% vs no-replan %.1f%%)",
				s.replanEffectiveness*100, s.noReplanSuccessRate*100),
		}
	case s.replanEffectiveness < replanIneffectiveThreshold:
		// Replans are ineffective → raise threshold to avoid unnecessary replans.
		newVal := clamp(prev*(1+adjFraction), minReplanThreshold, maxReplanThreshold)
		so.strategy.ReplanThreshold = StrategyParam{
			Value:    newVal,
			Previous: prev,
			Reason:   fmt.Sprintf("replan ineffective (%.1f%% success)", s.replanEffectiveness*100),
		}
	}
}

func (so *StrategyOptimizer) adjustToolPriorities(s optimizeStats) {
	adjFraction := so.cfg.MaxAdjustmentPercent / 100.0

	for tool, total := range s.toolTotal {
		if total < minToolObservations {
			continue
		}

		toolRate := float64(s.toolSuccess[tool]) / float64(total)

		current, exists := so.strategy.ToolPriorities[tool]
		prev := defaultToolPriority
		if exists {
			prev = current.Value
		}

		var newVal float64
		var reason string

		switch {
		case toolRate > toolBoostThreshold:
			newVal = clamp(prev*(1+adjFraction), minToolPriority, maxToolPriority)
			reason = fmt.Sprintf("tool highly successful (%.1f%%)", toolRate*100)
		case toolRate < toolReduceThreshold:
			newVal = clamp(prev*(1-adjFraction), minToolPriority, maxToolPriority)
			reason = fmt.Sprintf("tool underperforming (%.1f%%)", toolRate*100)
		default:
			// Middle range — no change, but initialise if first time.
			if !exists {
				so.strategy.ToolPriorities[tool] = StrategyParam{
					Value:    prev,
					Previous: prev,
					Reason:   "initial observation",
				}
			}
			continue
		}

		so.strategy.ToolPriorities[tool] = StrategyParam{
			Value:    newVal,
			Previous: prev,
			Reason:   reason,
		}
	}
}

// ---------------------------------------------------------------------------
// Revert
// ---------------------------------------------------------------------------

func (so *StrategyOptimizer) revertLocked(previousSuccessRate float64) {
	rt := so.strategy.ReplanThreshold
	// Only revert if a previous value was recorded (Previous > 0 means an
	// adjustment was made in a prior cycle). If Previous == 0, the parameter
	// was never adjusted, so there is nothing to revert to.
	if rt.Previous > 0 {
		so.strategy.ReplanThreshold = StrategyParam{
			Value:    rt.Previous,
			Previous: rt.Previous,
			Reason:   "reverted due to success rate decline",
		}
	}

	for tool, param := range so.strategy.ToolPriorities {
		so.strategy.ToolPriorities[tool] = StrategyParam{
			Value:    param.Previous,
			Previous: param.Previous,
			Reason:   "reverted due to success rate decline",
		}
	}

	so.strategy.Metrics.OverallSuccessRate = previousSuccessRate
}

// ---------------------------------------------------------------------------
// Persistence (YAML)
// ---------------------------------------------------------------------------

// SaveStrategy writes the current strategy to the given path. Thread-safe.
func (so *StrategyOptimizer) SaveStrategy(path string) error {
	so.mu.Lock()
	defer so.mu.Unlock()
	return so.saveStrategyLocked(path)
}

// saveStrategyLocked is the inner save routine; caller must hold mu.
func (so *StrategyOptimizer) saveStrategyLocked(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	data, err := yaml.Marshal(so.strategy)
	if err != nil {
		return fmt.Errorf("marshal strategy: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write strategy file: %w", err)
	}
	return nil
}

// IsHardControlEnabled returns whether the optimizer should directly override
// agent runtime parameters (replan threshold, tool priorities) rather than
// only providing soft prompt hints.
func (so *StrategyOptimizer) IsHardControlEnabled() bool {
	return so.cfg.HardControlEnabled
}

// GetReplanThreshold returns the current evolved replan threshold value,
// clamped to safe bounds. Returns 0 if no optimization has run yet (version <= 1).
func (so *StrategyOptimizer) GetReplanThreshold() float64 {
	so.mu.Lock()
	defer so.mu.Unlock()
	if so.strategy.Version <= 1 {
		return 0
	}
	v := so.strategy.ReplanThreshold.Value
	if v < minReplanThreshold {
		return minReplanThreshold
	}
	if v > maxReplanThreshold {
		return maxReplanThreshold
	}
	return v
}

// GetToolPriority returns the evolved priority for a specific tool.
// Returns defaultToolPriority (0.5) if the tool has no custom priority.
func (so *StrategyOptimizer) GetToolPriority(toolName string) float64 {
	so.mu.Lock()
	defer so.mu.Unlock()
	if p, ok := so.strategy.ToolPriorities[toolName]; ok {
		return p.Value
	}
	return defaultToolPriority
}

// GetStrategy returns a copy of the current strategy. Thread-safe.
func (so *StrategyOptimizer) GetStrategy() Strategy {
	so.mu.Lock()
	defer so.mu.Unlock()
	s := *so.strategy
	tp := make(map[string]StrategyParam, len(so.strategy.ToolPriorities))
	for k, v := range so.strategy.ToolPriorities {
		tp[k] = v
	}
	s.ToolPriorities = tp
	return s
}

// BuildPromptSection returns a human-readable strategy summary suitable for
// injection into the PLAN phase prompt. Returns empty string when there is
// nothing meaningful to report (version <= 1 means no optimization has run).
func (so *StrategyOptimizer) BuildPromptSection() string {
	so.mu.Lock()
	defer so.mu.Unlock()
	if so.strategy.Version <= 1 {
		return ""
	}
	var b strings.Builder
	b.WriteString("STRATEGY HINTS (from self-evolution):\n")
	fmt.Fprintf(&b, "- Replan threshold: %.2f (%s)\n",
		so.strategy.ReplanThreshold.Value, so.strategy.ReplanThreshold.Reason)
	if len(so.strategy.ToolPriorities) > 0 {
		b.WriteString("- Tool priority adjustments:\n")
		for tool, param := range so.strategy.ToolPriorities {
			if param.Value != defaultToolPriority {
				label := "neutral"
				if param.Value > defaultToolPriority+0.1 {
					label = "preferred"
				} else if param.Value < defaultToolPriority-0.1 {
					label = "less preferred"
				}
				fmt.Fprintf(&b, "  - %s: %.2f (%s, %s)\n", tool, param.Value, label, param.Reason)
			}
		}
	}
	fmt.Fprintf(&b, "- Historical success rate: %.0f%% (%d episodes)\n",
		so.strategy.Metrics.OverallSuccessRate*100, so.strategy.Metrics.EpisodesAnalyzed)
	return b.String()
}

// ApplyInsights ingests an InsightsReport and adjusts the strategy based on
// its findings. This allows historical pattern analysis to feed back into
// real-time cognitive parameter tuning. Thread-safe.
func (so *StrategyOptimizer) ApplyInsights(report *InsightsReport) int {
	if report == nil || report.TotalEpisodes == 0 {
		return 0
	}

	so.mu.Lock()
	defer so.mu.Unlock()

	applied := 0
	adjFraction := so.cfg.MaxAdjustmentPercent / 100.0

	// 1. Adjust tool priorities based on insights tool success rates.
	for _, ti := range report.TopTools {
		if ti.Uses < minToolObservations {
			continue
		}
		current, exists := so.strategy.ToolPriorities[ti.Name]
		prev := defaultToolPriority
		if exists {
			prev = current.Value
		}

		var newVal float64
		var reason string

		switch {
		case ti.SuccessRate > toolBoostThreshold:
			newVal = clamp(prev*(1+adjFraction), minToolPriority, maxToolPriority)
			reason = fmt.Sprintf("insights: high success (%.0f%% over %d uses)", ti.SuccessRate*100, ti.Uses)
		case ti.SuccessRate < toolReduceThreshold:
			newVal = clamp(prev*(1-adjFraction), minToolPriority, maxToolPriority)
			reason = fmt.Sprintf("insights: low success (%.0f%% over %d uses)", ti.SuccessRate*100, ti.Uses)
		default:
			continue
		}

		so.strategy.ToolPriorities[ti.Name] = StrategyParam{
			Value:    newVal,
			Previous: prev,
			Reason:   reason,
		}
		applied++
	}

	// 2. Adjust replan threshold based on average replan count and success rate.
	if report.TotalEpisodes >= 5 {
		prev := so.strategy.ReplanThreshold.Value

		if report.AvgReplanCount > 1.5 && report.SuccessRate < 0.5 {
			// Too many replans with low success → raise threshold (fewer replans).
			newVal := clamp(prev*(1+adjFraction), minReplanThreshold, maxReplanThreshold)
			so.strategy.ReplanThreshold = StrategyParam{
				Value:    newVal,
				Previous: prev,
				Reason:   fmt.Sprintf("insights: high replan (%.1f) + low success (%.0f%%)", report.AvgReplanCount, report.SuccessRate*100),
			}
			applied++
		} else if report.AvgReplanCount < 0.5 && report.SuccessRate > 0.8 {
			// Rarely replanning and high success → lower threshold to enable replans when needed.
			newVal := clamp(prev*(1-adjFraction*0.5), minReplanThreshold, maxReplanThreshold)
			so.strategy.ReplanThreshold = StrategyParam{
				Value:    newVal,
				Previous: prev,
				Reason:   fmt.Sprintf("insights: low replan (%.1f) + high success (%.0f%%)", report.AvgReplanCount, report.SuccessRate*100),
			}
			applied++
		}
	}

	if applied > 0 {
		so.strategy.UpdatedAt = time.Now()
		so.strategy.Version++

		slog.Info("strategy_optimizer: applied insights",
			"adjustments", applied,
			"version", so.strategy.Version,
		)

		if so.cfg.StrategyFile != "" {
			if err := so.saveStrategyLocked(so.cfg.StrategyFile); err != nil {
				slog.Error("strategy_optimizer: save after insights failed", "error", err)
			}
		}
	}

	return applied
}

// LoadStrategy reads a strategy from the given YAML path. Thread-safe.
func (so *StrategyOptimizer) LoadStrategy(path string) error {
	so.mu.Lock()
	defer so.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read strategy file: %w", err)
	}

	var loaded Strategy
	if err := yaml.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("unmarshal strategy: %w", err)
	}

	so.strategy = &loaded
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}
