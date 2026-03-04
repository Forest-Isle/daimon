package rl

import (
	"github.com/punkopunko/ironclaw/internal/config"
)

// RewardComponents holds the decomposed reward values.
type RewardComponents struct {
	TaskSuccess      float64
	Efficiency       float64
	Safety           float64
	UserSatisfaction float64
}

// Total computes the weighted sum of reward components.
func (r *RewardComponents) Total(cfg config.RewardConfig) float64 {
	return r.TaskSuccess*cfg.TaskSuccessWeight +
		r.Efficiency*cfg.EfficiencyWeight +
		r.Safety*cfg.SafetyWeight +
		r.UserSatisfaction*cfg.UserSatisfactionWeight
}

// ComputeToolReward computes the immediate reward for a tool execution.
func ComputeToolReward(succeeded bool, denied bool, durationMs int64) float64 {
	if denied {
		return -0.5
	}
	if !succeeded {
		return -1.0
	}
	// Base success reward with efficiency bonus
	reward := 1.0
	// Bonus for fast execution (< 5s)
	if durationMs < 5000 {
		reward += 0.1 * (1.0 - float64(durationMs)/5000.0)
	}
	return reward
}

// ComputeEpisodeReward computes the total reward for a completed episode.
func ComputeEpisodeReward(params EpisodeRewardParams, cfg config.RewardConfig) *RewardComponents {
	rc := &RewardComponents{}

	// Task success: +1 for success, -1 for failure
	if params.Succeeded {
		rc.TaskSuccess = 1.0
	} else {
		rc.TaskSuccess = -1.0
	}

	// Efficiency: based on duration and replan count
	if params.MaxDurationMs > 0 {
		rc.Efficiency = 1.0 - clamp(float64(params.DurationMs)/float64(params.MaxDurationMs), 0, 1)
	}
	// Penalty for replans
	rc.Efficiency -= 0.2 * float64(params.ReplanCount)
	rc.Efficiency = clamp(rc.Efficiency, -1, 1)

	// Safety: based on denied/failed ratio
	totalActions := params.SuccessCount + params.FailureCount + params.DeniedCount
	if totalActions > 0 {
		rc.Safety = 1.0 - float64(params.DeniedCount+params.FailureCount)/float64(totalActions)
	} else {
		rc.Safety = 0.5 // neutral if no actions
	}

	// User satisfaction: set externally via feedback
	rc.UserSatisfaction = params.UserFeedback

	return rc
}

// EpisodeRewardParams holds the raw values for computing episode reward.
type EpisodeRewardParams struct {
	Succeeded      bool
	DurationMs     int64
	MaxDurationMs  int64
	ReplanCount    int
	SuccessCount   int
	FailureCount   int
	DeniedCount    int
	UserFeedback   float64 // -1 to 1 (from 👎/👍)
}
