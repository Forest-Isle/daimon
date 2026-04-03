package rl

import (
	"context"
	"log/slog"
	"math"
	"math/rand"

	"github.com/Forest-Isle/IronClaw/internal/config"
)

// ContextualBandit implements Thompson Sampling with Beta distributions.
type ContextualBandit struct {
	storage    *Storage
	cfg        config.BanditConfig
	priorAlpha float64
	priorBeta  float64
}

// NewContextualBandit creates a new contextual bandit.
func NewContextualBandit(storage *Storage, cfg config.BanditConfig) *ContextualBandit {
	alpha := cfg.PriorAlpha
	beta := cfg.PriorBeta
	if alpha <= 0 {
		alpha = 1.0
	}
	if beta <= 0 {
		beta = 1.0
	}
	return &ContextualBandit{
		storage:    storage,
		cfg:        cfg,
		priorAlpha: alpha,
		priorBeta:  beta,
	}
}

// SelectTool selects a tool using Thompson Sampling.
func (b *ContextualBandit) SelectTool(ctx context.Context, state *RLState, toolNames []string) *ToolSelectionAction {
	if len(toolNames) == 0 {
		return nil
	}

	contextHash := state.ContextHash()
	bestTool := ""
	bestSample := -1.0
	bestIndex := 0

	// Thompson Sampling: sample from Beta distribution for each arm
	for i, toolName := range toolNames {
		alpha, beta, _, _, err := b.storage.GetBanditArm(ctx, contextHash, toolName)
		if err != nil {
			// Arm not seen before, use priors
			alpha = b.priorAlpha
			beta = b.priorBeta
		}

		// Sample from Beta(alpha, beta)
		sample := sampleBeta(alpha, beta)
		if sample > bestSample {
			bestSample = sample
			bestTool = toolName
			bestIndex = i
		}
	}

	return &ToolSelectionAction{
		ToolName:   bestTool,
		ToolIndex:  bestIndex,
		Confidence: bestSample,
	}
}

// Update updates the bandit arm statistics after observing a reward.
func (b *ContextualBandit) Update(ctx context.Context, state *RLState, toolName string, reward float64) error {
	contextHash := state.ContextHash()

	alpha, beta, pulls, totalReward, err := b.storage.GetBanditArm(ctx, contextHash, toolName)
	if err != nil {
		// First pull for this arm
		alpha = b.priorAlpha
		beta = b.priorBeta
		pulls = 0
		totalReward = 0
	}

	// Update Beta distribution parameters
	// Reward is in [-1, 1], map to [0, 1] for Beta update
	normalizedReward := (reward + 1.0) / 2.0
	if normalizedReward > 1.0 {
		normalizedReward = 1.0
	}
	if normalizedReward < 0.0 {
		normalizedReward = 0.0
	}

	alpha += normalizedReward
	beta += (1.0 - normalizedReward)
	pulls++
	totalReward += reward

	err = b.storage.UpdateBanditArm(ctx, contextHash, toolName, alpha, beta, pulls, totalReward)
	if err != nil {
		slog.Warn("bandit: failed to update arm", "tool", toolName, "err", err)
		return err
	}

	slog.Debug("bandit: arm updated", "tool", toolName, "alpha", alpha, "beta", beta, "pulls", pulls)
	return nil
}

// GetArmStats retrieves statistics for a specific arm.
func (b *ContextualBandit) GetArmStats(ctx context.Context, state *RLState, toolName string) (mean, variance float64, pulls int) {
	contextHash := state.ContextHash()
	alpha, beta, p, _, err := b.storage.GetBanditArm(ctx, contextHash, toolName)
	if err != nil {
		alpha = b.priorAlpha
		beta = b.priorBeta
		p = 0
	}

	// Beta distribution mean and variance
	mean = alpha / (alpha + beta)
	variance = (alpha * beta) / ((alpha + beta) * (alpha + beta) * (alpha + beta + 1))
	pulls = p
	return mean, variance, pulls
}

// sampleBeta samples from a Beta(alpha, beta) distribution using rejection sampling.
func sampleBeta(alpha, beta float64) float64 {
	if alpha <= 0 || beta <= 0 {
		return 0.5
	}

	// Use Gamma sampling: Beta(a,b) = Gamma(a) / (Gamma(a) + Gamma(b))
	x := sampleGamma(alpha, 1.0)
	y := sampleGamma(beta, 1.0)
	if x+y == 0 {
		return 0.5
	}
	return x / (x + y)
}

// sampleGamma samples from a Gamma(shape, scale) distribution.
func sampleGamma(shape, scale float64) float64 {
	if shape < 1 {
		// Use Marsaglia and Tsang's method with shape augmentation
		return sampleGamma(shape+1, scale) * math.Pow(rand.Float64(), 1.0/shape)
	}

	// Marsaglia and Tsang's method
	d := shape - 1.0/3.0
	c := 1.0 / math.Sqrt(9.0*d)

	for {
		x := rand.NormFloat64()
		v := 1.0 + c*x
		if v <= 0 {
			continue
		}
		v = v * v * v
		u := rand.Float64()
		if u < 1.0-0.0331*(x*x)*(x*x) {
			return d * v * scale
		}
		if math.Log(u) < 0.5*x*x+d*(1.0-v+math.Log(v)) {
			return d * v * scale
		}
	}
}
