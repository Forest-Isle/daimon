package evolution

// RewardInput holds the common signals used to compute a scalar reward
// for a completed cognitive episode. Both the live agent path and the
// offline trajectory bridge should use ComputeReward to keep scoring
// consistent.
type RewardInput struct {
	Succeeded    bool
	Progress     float64 // 0.0–1.0 progress from ObservationResult (live path)
	DurationMs   int64
	ReplanCount  int
	UserFeedback float64 // -1 to 1
}

// ComputeReward produces a unified scalar reward in roughly [-1.5, 1.0].
//
// Components:
//   - Base: +0.5 for success, -0.5 for failure
//   - Progress bonus: up to +0.25 based on OverallProgress
//   - Speed bonus: +0.1 if completed in under 60s
//   - Efficiency bonus: +0.05 if no replans were needed
//   - User feedback: up to +/-0.1
func ComputeReward(in RewardInput) float64 {
	reward := 0.0

	if in.Succeeded {
		reward += 0.5
	} else {
		reward -= 0.5
	}

	reward += in.Progress * 0.25

	if in.DurationMs > 0 && in.DurationMs < 60000 {
		reward += 0.1
	}

	if in.ReplanCount == 0 {
		reward += 0.05
	}

	reward += in.UserFeedback * 0.1

	return clampReward(reward, -1.5, 1.0)
}
