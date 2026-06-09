package config

import "time"

type LLMConfig struct {
	Provider  string      `yaml:"provider"`
	APIKey    string      `yaml:"api_key"`
	BaseURL   string      `yaml:"base_url"`
	Model     string      `yaml:"model"`
	MaxTokens int         `yaml:"max_tokens"`
	Retry     RetryConfig `yaml:"retry"`
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

// DiscordConfig holds Discord bot settings.
type DiscordConfig struct {
	Token          string   `yaml:"token"`
	AllowedUserIDs []string `yaml:"allowed_user_ids"`
}

// TUIConfig configures the TUI (terminal UI) channel.
type TUIConfig struct {
	AutoApprove bool   `yaml:"auto_approve"` // skip approval prompts
	Theme       string `yaml:"theme"`        // reserved for future use
}
