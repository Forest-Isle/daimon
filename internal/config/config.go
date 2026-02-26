package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	LLM       LLMConfig       `yaml:"llm"`
	Telegram  TelegramConfig  `yaml:"telegram"`
	Agent     AgentConfig     `yaml:"agent"`
	Store     StoreConfig     `yaml:"store"`
	Memory    MemoryConfig    `yaml:"memory"`
	Scheduler SchedulerConfig `yaml:"scheduler"`
	Tools     ToolsConfig     `yaml:"tools"`
	Server    ServerConfig    `yaml:"server"`
	Log       LogConfig       `yaml:"log"`
}

type LLMConfig struct {
	Provider  string `yaml:"provider"`
	APIKey    string `yaml:"api_key"`
	Model     string `yaml:"model"`
	MaxTokens int    `yaml:"max_tokens"`
}

type TelegramConfig struct {
	Token          string  `yaml:"token"`
	AllowedUserIDs []int64 `yaml:"allowed_user_ids"`
}

type AgentConfig struct {
	MaxIterations int    `yaml:"max_iterations"`
	SystemPrompt  string `yaml:"system_prompt"`
}

type StoreConfig struct {
	Path string `yaml:"path"`
}

type MemoryConfig struct {
	Enabled        bool   `yaml:"enabled"`
	EmbeddingModel string `yaml:"embedding_model"`
}

type SchedulerConfig struct {
	Enabled bool `yaml:"enabled"`
}

type ToolsConfig struct {
	Bash BashToolConfig `yaml:"bash"`
	File FileToolConfig `yaml:"file"`
	HTTP HTTPToolConfig `yaml:"http"`
}

type BashToolConfig struct {
	Enabled          bool          `yaml:"enabled"`
	Timeout          time.Duration `yaml:"timeout"`
	RequiresApproval bool          `yaml:"requires_approval"`
	BlockedCommands  []string      `yaml:"blocked_commands"`
}

type FileToolConfig struct {
	Enabled          bool `yaml:"enabled"`
	RequiresApproval bool `yaml:"requires_approval"`
}

type HTTPToolConfig struct {
	Enabled          bool          `yaml:"enabled"`
	RequiresApproval bool          `yaml:"requires_approval"`
	Timeout          time.Duration `yaml:"timeout"`
}

type ServerConfig struct {
	Addr    string `yaml:"addr"`
	Enabled bool   `yaml:"enabled"`
}

type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Substitute environment variables
	expanded := envVarPattern.ReplaceAllFunc(data, func(match []byte) []byte {
		varName := envVarPattern.FindSubmatch(match)[1]
		if val, ok := os.LookupEnv(string(varName)); ok {
			return []byte(val)
		}
		return match
	})

	cfg := defaultConfig()
	if err := yaml.Unmarshal(expanded, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &cfg, nil
}

func defaultConfig() Config {
	return Config{
		LLM: LLMConfig{
			Provider:  "claude",
			Model:     "claude-sonnet-4-20250514",
			MaxTokens: 8192,
		},
		Agent: AgentConfig{
			MaxIterations: 20,
		},
		Store: StoreConfig{
			Path: "./data/ironclaw.db",
		},
		Server: ServerConfig{
			Addr: ":8080",
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
		Tools: ToolsConfig{
			Bash: BashToolConfig{
				Enabled:          true,
				Timeout:          30 * time.Second,
				RequiresApproval: true,
			},
			File: FileToolConfig{
				Enabled: true,
			},
			HTTP: HTTPToolConfig{
				Enabled: true,
				Timeout: 30 * time.Second,
			},
		},
	}
}
