package rl

import (
	"context"
	"log/slog"

	"github.com/punkopunko/ironclaw/internal/config"
)

// Policy is the unified interface for all RL components.
type Policy struct {
	bandit  *ContextualBandit
	ppo     *PPO
	dqn     *DQN
	storage *Storage
	cfg     config.RLConfig
	enabled bool
}

// NewPolicy creates a new RL policy with all three levels.
func NewPolicy(storage *Storage, cfg config.RLConfig) *Policy {
	if !cfg.Enabled {
		return &Policy{enabled: false}
	}

	var bandit *ContextualBandit
	if cfg.Bandit.Enabled {
		bandit = NewContextualBandit(storage, cfg.Bandit)
	}

	var ppo *PPO
	if cfg.PPO.Enabled {
		ppo = NewPPO(cfg.PPO)
	}

	var dqn *DQN
	if cfg.DQN.Enabled {
		dqn = NewDQN(cfg.DQN)
	}

	return &Policy{
		bandit:  bandit,
		ppo:     ppo,
		dqn:     dqn,
		storage: storage,
		cfg:     cfg,
		enabled: true,
	}
}

// IsEnabled returns whether the RL system is enabled.
func (p *Policy) IsEnabled() bool {
	return p.enabled
}

// SelectTool uses the bandit to select a tool.
func (p *Policy) SelectTool(ctx context.Context, state *RLState, toolNames []string) *ToolSelectionAction {
	if !p.enabled || p.bandit == nil {
		return nil
	}
	return p.bandit.SelectTool(ctx, state, toolNames)
}

// UpdateToolSelection updates the bandit after tool execution.
func (p *Policy) UpdateToolSelection(ctx context.Context, state *RLState, toolName string, reward float64) error {
	if !p.enabled || p.bandit == nil {
		return nil
	}
	return p.bandit.Update(ctx, state, toolName, reward)
}

// SelectPlanStrategy uses PPO to suggest plan parameters.
func (p *Policy) SelectPlanStrategy(state *RLState) *PlanStrategyAction {
	if !p.enabled || p.ppo == nil {
		return &PlanStrategyAction{} // neutral bias
	}
	return p.ppo.SelectAction(state)
}

// SelectReplanAction uses DQN to decide on replan.
func (p *Policy) SelectReplanAction(state *RLState) ReplanActionType {
	if !p.enabled || p.dqn == nil {
		return ReplanActionContinue // default to continue
	}
	return p.dqn.SelectAction(state)
}

// RecordExperience adds an experience to the buffer (handled by trainer).
func (p *Policy) RecordExperience(exp Experience) {
	// This is a no-op; the trainer manages the experience buffer
	// This method exists for interface compatibility
}

// GetBandit returns the bandit instance (for trainer access).
func (p *Policy) GetBandit() *ContextualBandit {
	return p.bandit
}

// GetPPO returns the PPO instance (for trainer access).
func (p *Policy) GetPPO() *PPO {
	return p.ppo
}

// GetDQN returns the DQN instance (for trainer access).
func (p *Policy) GetDQN() *DQN {
	return p.dqn
}

// GetStorage returns the storage instance.
func (p *Policy) GetStorage() *Storage {
	return p.storage
}

// GetConfig returns the RL configuration.
func (p *Policy) GetConfig() config.RLConfig {
	return p.cfg
}

// SaveCheckpoint saves all policy weights to storage.
func (p *Policy) SaveCheckpoint(ctx context.Context, version int) error {
	if !p.enabled {
		return nil
	}

	metrics := make(map[string]float64)

	// Save PPO weights
	if p.ppo != nil {
		ppoWeights := p.ppo.GetWeights()
		err := p.storage.SaveCheckpoint(ctx, "ppo", version, StateDim, 3, ppoWeights, metrics)
		if err != nil {
			slog.Warn("policy: failed to save PPO checkpoint", "err", err)
		}
	}

	// Save DQN weights
	if p.dqn != nil {
		dqnWeights := p.dqn.GetWeights()
		metrics["epsilon"] = p.dqn.GetEpsilon()
		err := p.storage.SaveCheckpoint(ctx, "dqn", version, StateDim, NumReplanActions, dqnWeights, metrics)
		if err != nil {
			slog.Warn("policy: failed to save DQN checkpoint", "err", err)
		}
	}

	slog.Info("policy: checkpoint saved", "version", version)
	return nil
}

// LoadCheckpoint loads policy weights from storage.
func (p *Policy) LoadCheckpoint(ctx context.Context) error {
	if !p.enabled {
		return nil
	}

	// Load PPO weights
	if p.ppo != nil {
		weights, version, err := p.storage.LoadCheckpoint(ctx, "ppo")
		if err == nil {
			p.ppo.SetWeights(weights)
			slog.Info("policy: PPO checkpoint loaded", "version", version)
		}
	}

	// Load DQN weights
	if p.dqn != nil {
		weights, version, err := p.storage.LoadCheckpoint(ctx, "dqn")
		if err == nil {
			p.dqn.SetWeights(weights)
			slog.Info("policy: DQN checkpoint loaded", "version", version)
		}
	}

	return nil
}
