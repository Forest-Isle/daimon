package agent

import (
	"strings"

	"github.com/punkopunko/ironclaw/internal/rl"
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
func computeSimpleEpisodeReward(reflection *Reflection, obs *ObservationResult) float64 {
	if reflection == nil {
		return -0.5
	}
	reward := 0.0
	if reflection.Succeeded {
		reward += 1.0
	} else {
		reward -= 1.0
	}
	if obs != nil {
		reward += obs.OverallProgress * 0.5
	}
	return reward
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
