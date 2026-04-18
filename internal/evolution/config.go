package evolution

import "time"

// Config holds configuration for the self-evolution engine.
// The engine runs three feedback loops that make the agent smarter over time:
//   - PreferenceLearner: extracts user preferences from reflection outcomes
//   - SkillSynthesizer: detects repeated tool patterns and generates skill drafts
//   - StrategyOptimizer: tunes cognitive parameters based on success/failure statistics
type Config struct {
	Enabled bool `yaml:"enabled"`

	Preference  PreferenceConfig  `yaml:"preference"`
	Synthesizer SynthesizerConfig `yaml:"synthesizer"`
	Optimizer   OptimizerConfig   `yaml:"optimizer"`
	Router      RouterConfig      `yaml:"model_routing"`

	// PreferenceFile is the YAML path (relative to ~/.IronClaw/evolution/)
	// where learned preferences are persisted between sessions.
	PreferenceFile string `yaml:"preference_file"`

	// HookTimeout is the maximum duration for a single hook execution.
	// Hooks that exceed this timeout are cancelled and logged as warnings.
	HookTimeout time.Duration `yaml:"hook_timeout"`
}

// PreferenceConfig controls Loop 1: learning user preferences.
type PreferenceConfig struct {
	Enabled        bool    `yaml:"enabled"`
	MaxPreferences int     `yaml:"max_preferences"` // cap on stored preferences per user
	MinConfidence  float64 `yaml:"min_confidence"`  // minimum confidence to persist a preference
	LLMModel       string  `yaml:"llm_model"`       // model for preference classification; empty = use reflect model
}

// SynthesizerConfig controls Loop 2: auto-generating skills from patterns.
type SynthesizerConfig struct {
	Enabled          bool    `yaml:"enabled"`
	PatternThreshold int     `yaml:"pattern_threshold"` // min occurrences to trigger skill draft
	RewardThreshold  float64 `yaml:"reward_threshold"`  // min avg reward for pattern to qualify
	DraftsDir        string  `yaml:"drafts_dir"`        // relative to ~/.IronClaw/skills/
	AutoNotify       bool    `yaml:"auto_notify"`       // notify user on next session start
}

// OptimizerConfig controls Loop 3: tuning cognitive agent parameters.
type OptimizerConfig struct {
	Enabled              bool    `yaml:"enabled"`
	UpdateInterval       int     `yaml:"update_interval"`        // evaluate every N episodes
	MaxAdjustmentPercent float64 `yaml:"max_adjustment_percent"` // max % change per cycle
	RevertThreshold      float64 `yaml:"revert_threshold"`       // revert if success drops by this %
	StrategyFile         string  `yaml:"strategy_file"`          // relative to ~/.IronClaw/evolution/
	HardControlEnabled   bool    `yaml:"hard_control_enabled"`   // when true, optimizer values directly override agent params (not just prompt hints)
}

// DefaultConfig returns sensible defaults with the engine disabled.
func DefaultConfig() Config {
	return Config{
		Enabled:        false,
		HookTimeout:    10 * time.Second,
		PreferenceFile: "preferences.yaml",
		Router:         DefaultRouterConfig(),
		Preference: PreferenceConfig{
			Enabled:        true,
			MaxPreferences: 100,
			MinConfidence:  0.3,
		},
		Synthesizer: SynthesizerConfig{
			Enabled:          true,
			PatternThreshold: 3,
			RewardThreshold:  0.5,
			DraftsDir:        "drafts",
			AutoNotify:       true,
		},
		Optimizer: OptimizerConfig{
			Enabled:              true,
			UpdateInterval:       10,
			MaxAdjustmentPercent: 10,
			RevertThreshold:      0.15,
			StrategyFile:         "strategy.yaml",
		},
	}
}
