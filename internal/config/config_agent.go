package config

type AgentConfig struct {
	MaxIterations   int               `yaml:"max_iterations"`
	SystemPrompt    string            `yaml:"system_prompt"`
	Personality     string            `yaml:"-"`    // Soul.md → persona/style (injected by userdir)
	PersistentRules string            `yaml:"-"`    // Memory.md → long-term rules (injected by userdir)
	Mode            string            `yaml:"mode"` // "simple" | "unified" (also accepts "cognitive" for backward compat)
	Cognitive       CognitiveConfig   `yaml:"cognitive"`
	Compression     CompressionConfig `yaml:"compression"`
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

// CognitiveConfig holds configuration for the unified agent loop.
type CognitiveConfig struct {
	ReflectModel           string `yaml:"reflect_model"`
	MaxParallelTools       int    `yaml:"max_parallel_tools"`       // default 3
	ApprovalTimeoutSeconds int    `yaml:"approval_timeout_seconds"` // default 120
	StreamingEnabled       bool   `yaml:"streaming_enabled"`        // enable channel-based streaming pipeline (default false)
}
