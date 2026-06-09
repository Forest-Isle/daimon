package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	LLM           LLMConfig           `yaml:"llm"`
	Telegram      TelegramConfig      `yaml:"telegram"`
	Discord       DiscordConfig       `yaml:"discord"`
	TUI           TUIConfig           `yaml:"tui"`
	Agent         AgentConfig         `yaml:"agent"`
	Store         StoreConfig         `yaml:"store"`
	Memory        MemoryConfig        `yaml:"memory"`
	Scheduler     SchedulerConfig     `yaml:"scheduler"`
	Tools         ToolsConfig         `yaml:"tools"`
	Server        ServerConfig        `yaml:"server"`
	Health        HealthConfig        `yaml:"health"`
	Log           LogConfig           `yaml:"log"`
	Observability ObservabilityConfig `yaml:"observability"`
	Skills        SkillsConfig        `yaml:"skills"`
	Agents        AgentsConfig        `yaml:"agents"`
	Permissions   PermissionsConfig   `yaml:"permissions"`
	Sandbox       SandboxConfig       `yaml:"sandbox"`
	Hooks         HooksConfig         `yaml:"hooks"`

}

var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// ExpandEnv replaces ${VAR} placeholders in data with values from the environment.
// Placeholders with no matching env var are left unchanged.
func ExpandEnv(data []byte) []byte {
	return envVarPattern.ReplaceAllFunc(data, func(match []byte) []byte {
		varName := envVarPattern.FindSubmatch(match)[1]
		if val, ok := os.LookupEnv(string(varName)); ok {
			return []byte(val)
		}
		return match
	})
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Substitute environment variables
	expanded := ExpandEnv(data)

	cfg := defaultConfig()
	if err := yaml.Unmarshal(expanded, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Warn about unknown top-level YAML keys (typos, removed features like RL).
	CheckUnknownKeys(expanded)

	// Overlay project-level and local-level config if available.
	// The explicit path acts as user-level; project (.ironclaw/) and
	// local (.ironclaw/local.yaml) configs are discovered from cwd.
	if wd, wdErr := os.Getwd(); wdErr == nil {
		overlayHierarchy(&cfg, wd)
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
			Retry: RetryConfig{
				MaxRetries: 3,
				BaseDelay:  1 * time.Second,
				MaxDelay:   30 * time.Second,
			},
		},
		Telegram: TelegramConfig{
			Timeout: 30 * time.Second,
		},
		Agent: AgentConfig{
			MaxIterations: 20,
			Mode:          "simple",
			Cognitive: CognitiveConfig{
				MaxParallelTools:       3,
				ApprovalTimeoutSeconds: 120,
			},
			Compression: CompressionConfig{
				Strategy: "layered",
				Layers: CompressionLayers{
					ToolOutputReducePct: 30,
					SummarizePct:        50,
					EmergencyPct:        90,
				},
				TokenEstimateRatio: 0.25,
			},
		},
		Memory: MemoryConfig{
			Enabled: true,
		},
		Store: StoreConfig{
			Path: "./data/ironclaw.db",
		},
		Health: HealthConfig{
			Port: 9090,
		},
		Server: ServerConfig{
			Addr:          ":8080",
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
		Scheduler: SchedulerConfig{
			Enabled:      true,
			PollInterval: 30 * time.Second,
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
			Verify: VerifyConfig{
				Enabled: true,
			},
			MCP: MCPConfig{
				PollInterval: 30 * time.Second,
			},
			ConcurrentExecution: ConcurrentExecutionConfig{
				Enabled:        true,
				MaxConcurrency: 4,
			},
			ResultPersistence: ResultPersistenceConfig{
				Enabled:        true,
				ThresholdBytes: 8192,
				PreviewChars:   2000,
				TTLHours:       24,
			},
		},
		Skills: SkillsConfig{
			Enabled: true,
		},
		Agents: AgentsConfig{
			Enabled: true,
		},
		Permissions: PermissionsConfig{
			Default: "ask",
		},
		Sandbox: SandboxConfig{
			Enabled: false,
			Bash: BashSandboxConfig{
				Backend: "host",
				Docker: DockerSandboxConfig{
					Image:       "ironclaw-sandbox:latest",
					Network:     "none",
					MemoryLimit: "512m",
					CPULimit:    "1.0",
					IdleTimeout: 30 * time.Minute,
				},
			},
			Network: NetworkConfig{
				Mode: "blacklist",
			},
		},
	}
}
