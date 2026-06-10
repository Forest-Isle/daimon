package config

import "time"

// ModelRoles maps role names (opus, sonnet, haiku) to actual model IDs.
// Used by /model panel and setup wizard. If unset, official defaults are shown.
type ModelRoles struct {
	Opus   string `yaml:"opus"`
	Sonnet string `yaml:"sonnet"`
	Haiku  string `yaml:"haiku"`
}

type LLMConfig struct {
	Provider       string      `yaml:"provider"`
	APIKey         string      `yaml:"api_key"`
	BaseURL        string      `yaml:"base_url"`
	Model          string      `yaml:"model"`
	Models         ModelRoles  `yaml:"models"`
	MaxTokens      int         `yaml:"max_tokens"`
	ThinkingBudget int         `yaml:"thinking_budget"` // 0 = disabled; >0 enables extended thinking with this token budget
	Retry          RetryConfig `yaml:"retry"`
}

// RetryConfig controls retry behavior for LLM API calls.
type RetryConfig struct {
	MaxRetries int           `yaml:"max_retries"`
	BaseDelay  time.Duration `yaml:"base_delay"`
	MaxDelay   time.Duration `yaml:"max_delay"`
}

type TelegramConfig struct {
	Token          string        `yaml:"token"`
	AllowedUserIDs []int64       `yaml:"allowed_user_ids"`
	Timeout        time.Duration `yaml:"timeout"` // long-polling timeout for update retrieval; default: 30s
}

// TUIConfig configures the TUI (terminal UI) channel.
type TUIConfig struct {
	AutoApprove bool   `yaml:"auto_approve"` // skip approval prompts
	Theme       string `yaml:"theme"`        // reserved for future use
}
