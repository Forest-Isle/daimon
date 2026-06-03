package config

type AgentConfig struct {
	MaxIterations        int                        `yaml:"max_iterations"`
	SystemPrompt         string                     `yaml:"system_prompt"`
	Personality          string                     `yaml:"-"`    // Soul.md → persona/style (injected by userdir)
	PersistentRules      string                     `yaml:"-"`    // Memory.md → long-term rules (injected by userdir)
	Mode                 string                     `yaml:"mode"` // "simple" | "cognitive"
	Cognitive            CognitiveConfig            `yaml:"cognitive"`
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
	ToolOutputReducePct int `yaml:"tool_output_reduce_pct"`
	SummarizePct        int `yaml:"summarize_pct"`
	EmergencyPct        int `yaml:"emergency_pct"`
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
	StreamingEnabled       bool    `yaml:"streaming_enabled"`        // enable channel-based streaming pipeline (default false)
}
