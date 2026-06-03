package evolution

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Brain unifies the three evolution loops (Preference, Strategy, Skill)
// with cross-loop feedback and coordinated insight application.
type Brain struct {
	mu sync.RWMutex

	preference  *PreferenceLearner
	optimizer   *StrategyOptimizer
	synthesizer *SkillSynthesizer

	// Cross-loop feedback channels
	skillToPreference chan SkillFeedback    // skill activation affects preferences
	strategyToSkill   chan StrategyFeedback // strategy changes affect skill priorities

	// Unified metrics
	metrics BrainMetrics
}

// SkillFeedback carries information from skill synthesis to preference learning.
type SkillFeedback struct {
	SkillName string
	ToolsUsed []string
	Activated bool
	AvgReward float64
}

// StrategyFeedback carries strategy changes to skill synthesis.
type StrategyFeedback struct {
	ToolPriorities  map[string]float64
	ReplanThreshold float64
}

// BrainMetrics tracks unified evolution performance.
type BrainMetrics struct {
	TotalEpisodes         int64
	PreferenceUpdates     int64
	StrategyOptimizations int64
	SkillsActivated       int64
	InsightCycles         int64
	LastInsightAt         time.Time
	HealthScore           float64 // 0.0-1.0
}

// NewBrain creates a Brain that coordinates the three evolution loops.
// Any loop may be nil if not configured.
func NewBrain(pref *PreferenceLearner, opt *StrategyOptimizer, synth *SkillSynthesizer) *Brain {
	return &Brain{
		preference:        pref,
		optimizer:         opt,
		synthesizer:       synth,
		skillToPreference: make(chan SkillFeedback, 10),
		strategyToSkill:   make(chan StrategyFeedback, 10),
		metrics:           BrainMetrics{HealthScore: 1.0},
	}
}

// OnEpisodeComplete processes an episode across all loops with cross-feedback.
func (b *Brain) OnEpisodeComplete(ctx context.Context, event EpisodeEvent) {
	b.mu.Lock()
	b.metrics.TotalEpisodes++
	b.mu.Unlock()

	// Feed all three loops.
	if b.preference != nil {
		b.preference.OnEpisodeComplete(ctx, event)
		b.mu.Lock()
		b.metrics.PreferenceUpdates++
		b.mu.Unlock()
	}

	if b.optimizer != nil {
		b.optimizer.OnEpisodeComplete(ctx, event)
	}

	if b.synthesizer != nil {
		b.synthesizer.OnEpisodeComplete(ctx, event)
	}

	// Cross-feedback: push strategy state to skill synthesizer (non-blocking).
	if b.optimizer != nil && b.synthesizer != nil {
		select {
		case b.strategyToSkill <- StrategyFeedback{
			ToolPriorities:  b.optimizer.GetToolPriorities(),
			ReplanThreshold: b.optimizer.GetReplanThreshold(),
		}:
		default:
		}
	}
}

// ApplyInsights applies a unified insights report across all loops.
func (b *Brain) ApplyInsights(report *InsightsReport) {
	b.mu.Lock()
	b.metrics.InsightCycles++
	b.metrics.LastInsightAt = time.Now()
	b.mu.Unlock()

	if b.preference != nil {
		b.preference.ApplyInsights(report)
	}
	if b.optimizer != nil {
		b.optimizer.ApplyInsights(report)
	}

	slog.Info("evolution brain: insights applied",
		"total_episodes", report.TotalEpisodes,
		"success_rate", report.SuccessRate,
	)
}

// GetMetrics returns a snapshot of brain metrics.
func (b *Brain) GetMetrics() BrainMetrics {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.metrics
}

// DrainFeedback processes pending cross-loop feedback.
// Should be called periodically (e.g., in the insights cycle).
func (b *Brain) DrainFeedback() {
	for {
		select {
		case fb := <-b.skillToPreference:
			if b.preference != nil && fb.Activated {
				for _, tool := range fb.ToolsUsed {
					b.preference.BoostTool(tool, fb.AvgReward)
				}
			}
		case fb := <-b.strategyToSkill:
			if b.synthesizer != nil {
				b.synthesizer.SetToolPriorities(fb.ToolPriorities)
			}
		default:
			return
		}
	}
}

// Preference returns the preference learner (may be nil).
func (b *Brain) Preference() *PreferenceLearner { return b.preference }

// Optimizer returns the strategy optimizer (may be nil).
func (b *Brain) Optimizer() *StrategyOptimizer { return b.optimizer }

// Synthesizer returns the skill synthesizer (may be nil).
func (b *Brain) Synthesizer() *SkillSynthesizer { return b.synthesizer }

// ContainsHook returns true if h is one of the hooks already managed by the Brain
// (preference learner, strategy optimizer, or skill synthesizer). Callers that
// iterate engine hooks should skip any hook for which ContainsHook returns true
// when brain.OnEpisodeComplete has already been called — otherwise the hook
// receives duplicate events.
func (b *Brain) ContainsHook(h Hook) bool {
	if b == nil {
		return false
	}
	// Compare interface pointers — these are the exact same instances registered
	// as hooks AND wired into the brain.
	if b.preference != nil && h == b.preference {
		return true
	}
	if b.optimizer != nil && h == b.optimizer {
		return true
	}
	if b.synthesizer != nil && h == b.synthesizer {
		return true
	}
	return false
}
