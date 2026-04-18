package agent

import (
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/evolution"
	"github.com/Forest-Isle/IronClaw/internal/rl"
)

// buildInitialRLState creates an RLState from a CognitiveState after the PERCEIVE phase.
func buildInitialRLState(state *CognitiveState, toolCount int) *rl.RLState {
	return rl.BuildStateFromContext(rl.StateParams{
		Complexity:     string(state.Goal.Complexity),
		MemoryCount:    len(state.RelevantMemories),
		KnowledgeCount: len(state.KnowledgeContext),
		GraphCount:     len(state.GraphContext),
		HistoryLength:  len(state.RecentHistory),
		ToolCount:      toolCount,
		HasSkills:      state.Skills != "",
		HasAgents:      state.Agents != "",
		HasPersonality: state.Personality != "",
		WordCount:      len(strings.Fields(state.UserMessage)),
	})
}

// updateRLStateWithPlan updates the RL state with plan-phase features.
func updateRLStateWithPlan(s *rl.RLState, plan *TaskPlan) {
	s.SubTaskCount = normalizeRL(float64(len(plan.SubTasks)), 10)
	s.PlanConfidence = clampRL(plan.OverallConfidence, 0, 1)
	s.ReplanCount = normalizeRL(float64(plan.ReplanCount), 5)
}

// updateRLStateWithObservation updates the RL state with observation results.
func updateRLStateWithObservation(s *rl.RLState, obs *ObservationResult) {
	s.SuccessCount = normalizeRL(float64(obs.SuccessCount), 10)
	s.FailureCount = normalizeRL(float64(obs.FailureCount), 10)
	s.DeniedCount = normalizeRL(float64(obs.DeniedCount), 10)
	s.Progress = clampRL(obs.OverallProgress, 0, 1)
	s.ErrorPatternCnt = normalizeRL(float64(len(obs.ErrorPatterns)), 5)
}

// computeSimpleEpisodeReward returns a scalar reward for the entire episode.
// Delegates to evolution.ComputeReward for consistency with the offline
// trajectory bridge.
func computeSimpleEpisodeReward(reflection *Reflection, obs *ObservationResult, durationMs int64, replanCount int, userFeedback float64) float64 {
	if reflection == nil {
		return -0.5
	}
	progress := 0.0
	if obs != nil {
		progress = obs.OverallProgress
	}
	return evolution.ComputeReward(evolution.RewardInput{
		Succeeded:    reflection.Succeeded,
		Progress:     progress,
		DurationMs:   durationMs,
		ReplanCount:  replanCount,
		UserFeedback: userFeedback,
	})
}

// computeReflectionBonus returns an additional reward based on the richness
// of the reflection output. Richer reflections (with lessons, adjustments)
// indicate better self-awareness, which deserves positive reinforcement.
func computeReflectionBonus(reflection *Reflection) float64 {
	if reflection == nil {
		return 0.0
	}
	bonus := 0.0
	if len(reflection.LessonsLearned) > 0 {
		bonus += 0.15
	}
	if reflection.SuggestedAdjustment != "" {
		bonus += 0.05
	}
	return bonus
}

func normalizeRL(val, maxVal float64) float64 {
	if maxVal <= 0 {
		return 0
	}
	r := val / maxVal
	if r > 1 {
		return 1
	}
	if r < 0 {
		return 0
	}
	return r
}

func clampRL(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// applyDQNReplanAdjustment blends DQN replan output with LLM confidence.
// Returns the adjusted confidence and whether the DQN recommends aborting.
// When dqnWeight is 0, LLM confidence passes through unchanged.
func applyDQNReplanAdjustment(llmConfidence float64, dqnAction rl.ReplanActionType, dqnWeight float64) (adjustedConfidence float64, shouldAbort bool) {
	if dqnWeight <= 0 {
		return llmConfidence, false
	}
	if dqnWeight > 1 {
		dqnWeight = 1
	}

	switch dqnAction {
	case rl.ReplanActionAbort:
		// DQN recommends abort — confidence is not adjusted; caller must handle abort.
		return llmConfidence, true
	case rl.ReplanActionContinue:
		// DQN says "continue" → blend in a high-confidence signal (1.0).
		llmWeight := 1.0 - dqnWeight
		return clampRL(llmConfidence*llmWeight+1.0*dqnWeight, 0, 1), false
	case rl.ReplanActionAdjust:
		// DQN says "adjust" → blend in a neutral signal (0.5).
		llmWeight := 1.0 - dqnWeight
		return clampRL(llmConfidence*llmWeight+0.5*dqnWeight, 0, 1), false
	default:
		return llmConfidence, false
	}
}
