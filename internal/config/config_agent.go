package config

type AgentConfig struct {
	MaxIterations        int                        `yaml:"max_iterations"`
	SystemPrompt         string                     `yaml:"system_prompt"`
	Personality          string                     `yaml:"-"`    // Soul.md → persona/style (injected by userdir)
	PersistentRules      string                     `yaml:"-"`    // Memory.md → long-term rules (injected by userdir)
	Mode                 string                     `yaml:"mode"` // "simple" | "cognitive"
	Cognitive            CognitiveConfig            `yaml:"cognitive"`
	RL                   RLConfig                   `yaml:"rl"`
	Compression          CompressionConfig          `yaml:"compression"`
	SpeculativeExecution SpeculativeExecutionConfig `yaml:"speculative_execution"`
	Team                 TeamConfig                 `yaml:"team"`
}

// TeamConfig configures the Agent Teams coordination system.
type TeamConfig struct {
	Enabled    bool   `yaml:"enabled"`
	MaxWorkers int    `yaml:"max_workers"`
	Model      string `yaml:"model"`
}

// SpeculativeExecutionConfig controls launching read-only tools during streaming
// before the model finishes its response.
type SpeculativeExecutionConfig struct {
	Enabled     bool `yaml:"enabled"`
	MaxInFlight int  `yaml:"max_in_flight"`
}

// CompressionConfig controls the context compression strategy.
type CompressionConfig struct {
	Strategy           string            `yaml:"strategy"` // "layered" | "legacy"
	Layers             CompressionLayers `yaml:"layers"`
	TokenEstimateRatio float64           `yaml:"token_estimate_ratio"`
}

// CompressionLayers defines thresholds for each compression layer.
type CompressionLayers struct {
	ToolEvictionPct int `yaml:"tool_eviction_pct"`
	SummarizePct    int `yaml:"summarize_pct"`
	SlimPromptPct   int `yaml:"slim_prompt_pct"`
	EmergencyPct    int `yaml:"emergency_pct"`
}

// CognitiveConfig holds configuration for the five-step cognitive agent loop.
type CognitiveConfig struct {
	PlanModel              string  `yaml:"plan_model"`
	ReflectModel           string  `yaml:"reflect_model"`
	ConfidenceThreshold    float64 `yaml:"confidence_threshold"`     // default 0.6
	MaxParallelTools       int     `yaml:"max_parallel_tools"`       // default 3
	MaxReplanAttempts      int     `yaml:"max_replan_attempts"`      // default 2
	PlanMaxTokens          int     `yaml:"plan_max_tokens"`          // default 2048
	ReflectMaxTokens       int     `yaml:"reflect_max_tokens"`       // default 1024
	ApprovalTimeoutSeconds int     `yaml:"approval_timeout_seconds"` // default 120
}

// RLConfig holds configuration for the reinforcement learning system.
type RLConfig struct {
	Enabled             bool         `yaml:"enabled"`
	ColdStartEpisodes   int          `yaml:"cold_start_episodes"`
	ExplorationRate     float64      `yaml:"exploration_rate"`
	ExplorationDecay    float64      `yaml:"exploration_decay"`
	UpdateFrequency     int          `yaml:"update_frequency"`
	CheckpointFrequency int          `yaml:"checkpoint_frequency"`
	Bandit              BanditConfig `yaml:"bandit"`
	PPO                 PPOConfig    `yaml:"ppo"`
	DQN                 DQNConfig    `yaml:"dqn"`
	Reward              RewardConfig `yaml:"reward"`
}

// BanditConfig configures the Contextual Bandit for tool selection.
type BanditConfig struct {
	Enabled    bool    `yaml:"enabled"`
	PriorAlpha float64 `yaml:"prior_alpha"`
	PriorBeta  float64 `yaml:"prior_beta"`
}

// PPOConfig configures Proximal Policy Optimization for plan strategy.
type PPOConfig struct {
	Enabled      bool    `yaml:"enabled"`
	LearningRate float64 `yaml:"learning_rate"`
	ClipEpsilon  float64 `yaml:"clip_epsilon"`
	Epochs       int     `yaml:"epochs"`
	BatchSize    int     `yaml:"batch_size"`
	Gamma        float64 `yaml:"gamma"`
	GAELambda    float64 `yaml:"gae_lambda"`
}

// DQNConfig configures Deep Q-Network for replan decisions.
type DQNConfig struct {
	Enabled          bool    `yaml:"enabled"`
	LearningRate     float64 `yaml:"learning_rate"`
	Gamma            float64 `yaml:"gamma"`
	EpsilonStart     float64 `yaml:"epsilon_start"`
	EpsilonEnd       float64 `yaml:"epsilon_end"`
	EpsilonDecay     float64 `yaml:"epsilon_decay"`
	TargetUpdateFreq int     `yaml:"target_update_freq"`
	BufferSize       int     `yaml:"buffer_size"`
	ReplanWeight     float64 `yaml:"replan_weight"` // DQN influence on replan decision (0-1, default 0.3)
}

// RewardConfig configures reward weights for the RL system.
type RewardConfig struct {
	TaskSuccessWeight      float64 `yaml:"task_success_weight"`
	EfficiencyWeight       float64 `yaml:"efficiency_weight"`
	SafetyWeight           float64 `yaml:"safety_weight"`
	UserSatisfactionWeight float64 `yaml:"user_satisfaction_weight"`
}
