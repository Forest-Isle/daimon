package nn

import (
	"math"
)

// MSELoss computes mean squared error and its gradient.
func MSELoss(predicted, target []float64) (float64, []float64) {
	n := len(predicted)
	if n == 0 {
		return 0, nil
	}
	loss := 0.0
	grad := make([]float64, n)
	for i := 0; i < n; i++ {
		diff := predicted[i] - target[i]
		loss += diff * diff
		grad[i] = 2 * diff / float64(n)
	}
	return loss / float64(n), grad
}

// HuberLoss computes the Huber loss (smooth L1) and its gradient.
func HuberLoss(predicted, target []float64, delta float64) (float64, []float64) {
	n := len(predicted)
	if n == 0 {
		return 0, nil
	}
	loss := 0.0
	grad := make([]float64, n)
	for i := 0; i < n; i++ {
		diff := predicted[i] - target[i]
		absDiff := math.Abs(diff)
		if absDiff <= delta {
			loss += 0.5 * diff * diff
			grad[i] = diff / float64(n)
		} else {
			loss += delta*(absDiff-0.5*delta)
			if diff > 0 {
				grad[i] = delta / float64(n)
			} else {
				grad[i] = -delta / float64(n)
			}
		}
	}
	return loss / float64(n), grad
}

// PolicyGradientLoss computes the policy gradient loss: -sum(log_prob * advantage).
func PolicyGradientLoss(logProbs, advantages []float64) (float64, []float64) {
	n := len(logProbs)
	if n == 0 {
		return 0, nil
	}
	loss := 0.0
	grad := make([]float64, n)
	for i := 0; i < n; i++ {
		loss -= logProbs[i] * advantages[i]
		grad[i] = -advantages[i] / float64(n)
	}
	return loss / float64(n), grad
}

// ClippedSurrogateLoss computes the PPO clipped surrogate objective.
// ratio = pi_new / pi_old, advantage = GAE advantage estimate.
func ClippedSurrogateLoss(ratios, advantages []float64, clipEpsilon float64) (float64, []float64) {
	n := len(ratios)
	if n == 0 {
		return 0, nil
	}
	loss := 0.0
	grad := make([]float64, n)
	for i := 0; i < n; i++ {
		surr1 := ratios[i] * advantages[i]
		clipped := clampF(ratios[i], 1-clipEpsilon, 1+clipEpsilon) * advantages[i]
		// Take the minimum (pessimistic bound)
		if surr1 < clipped {
			loss -= surr1
			grad[i] = -advantages[i] / float64(n)
		} else {
			loss -= clipped
			// Gradient is zero when clipped
			if ratios[i] >= 1-clipEpsilon && ratios[i] <= 1+clipEpsilon {
				grad[i] = -advantages[i] / float64(n)
			}
		}
	}
	return loss / float64(n), grad
}

// ValueLoss computes the value function loss (MSE between predicted and target values).
func ValueLoss(predicted, returns []float64) (float64, []float64) {
	return MSELoss(predicted, returns)
}

// EntropyBonus computes the entropy of a probability distribution (for exploration).
func EntropyBonus(probs []float64) float64 {
	entropy := 0.0
	for _, p := range probs {
		if p > 1e-8 {
			entropy -= p * math.Log(p)
		}
	}
	return entropy
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
