package config

// HooksConfig configures the hook event system.
type HooksConfig struct {
	PreToolUse    []HookHandlerConfig `yaml:"pre_tool_use"`
	PostToolUse   []HookHandlerConfig `yaml:"post_tool_use"`
	OnUserMessage []HookHandlerConfig `yaml:"on_user_message"`
}

// HookHandlerConfig configures a single hook handler.
type HookHandlerConfig struct {
	Type   string         `yaml:"type"`
	Config map[string]any `yaml:"config"`
}

// SkillsConfig configures the skill system.
type SkillsConfig struct {
	Enabled   bool     `yaml:"enabled"`    // default: true
	ExtraDirs []string `yaml:"extra_dirs"` // additional skill directories
}

// AgentsConfig configures the multi-agent collaboration system.
type AgentsConfig struct {
	Enabled     bool              `yaml:"enabled"`
	ExtraDirs   []string          `yaml:"extra_dirs"`  // additional agent spec directories
	Definitions []AgentDefinition `yaml:"definitions"` // inline agent definitions
	Debate      DebateSettings    `yaml:"debate"`      // debate mode settings
}

// DebateSettings configures the debate mode for multi-agent collaboration.
type DebateSettings struct {
	MaxRounds  int  `yaml:"max_rounds"`  // max debate rounds, default 3
	AutoDetect bool `yaml:"auto_detect"` // auto-detect debate scenarios
}

// AgentDefinition is an inline agent spec in the config file.
type AgentDefinition struct {
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	SystemPrompt  string   `yaml:"system_prompt"`
	Model         string   `yaml:"model"`
	MaxTokens     int      `yaml:"max_tokens"`
	MaxIterations int      `yaml:"max_iterations"`
	Tools         []string `yaml:"tools"`
	Tags          []string `yaml:"tags"`
	Mode          string   `yaml:"mode"`
}
