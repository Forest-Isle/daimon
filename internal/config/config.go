package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Forest-Isle/IronClaw/internal/evolution"
)

type Config struct {
	LLM           LLMConfig           `yaml:"llm"`
	Telegram      TelegramConfig      `yaml:"telegram"`
	Discord       DiscordConfig       `yaml:"discord"`
	TUI           TUIConfig           `yaml:"tui"`
	Agent         AgentConfig         `yaml:"agent"`
	Store         StoreConfig         `yaml:"store"`
	Memory        MemoryConfig        `yaml:"memory"`
	Knowledge     KnowledgeConfig     `yaml:"knowledge"` // Phase 2 placeholder
	Graph         GraphConfig         `yaml:"graph"`     // Phase 3 placeholder
	Scheduler     SchedulerConfig     `yaml:"scheduler"`
	Tools         ToolsConfig         `yaml:"tools"`
	Server        ServerConfig        `yaml:"server"`
	Dashboard     DashboardConfig     `yaml:"dashboard"`
	Health        HealthConfig        `yaml:"health"`
	Log           LogConfig           `yaml:"log"`
	Observability ObservabilityConfig `yaml:"observability"`
	Skills        SkillsConfig        `yaml:"skills"`
	Agents        AgentsConfig        `yaml:"agents"`
	Permissions   PermissionsConfig   `yaml:"permissions"`
	Sandbox       SandboxConfig       `yaml:"sandbox"`
	Hooks         HooksConfig         `yaml:"hooks"`
	RateLimit     RateLimitConfig     `yaml:"rate_limit"`
	Evolution     evolution.Config    `yaml:"evolution"`
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
		Agent: AgentConfig{
			MaxIterations: 20,
			Mode:          "simple",
			Cognitive: CognitiveConfig{
				StreamingEnabled:       false,
				ConfidenceThreshold:    0.6,
				MaxParallelTools:       3,
				MaxReplanAttempts:      2,
				PlanMaxTokens:          2048,
				ReflectMaxTokens:       1024,
				ApprovalTimeoutSeconds: 120,
			},
			RL: RLConfig{
				Enabled:             false,
				ColdStartEpisodes:   1000,
				ExplorationRate:     0.2,
				ExplorationDecay:    0.9995,
				UpdateFrequency:     10,
				CheckpointFrequency: 100,
				Bandit: BanditConfig{
					Enabled:    true,
					PriorAlpha: 1.0,
					PriorBeta:  1.0,
				},
				PPO: PPOConfig{
					Enabled:      true,
					LearningRate: 0.0003,
					ClipEpsilon:  0.2,
					Epochs:       4,
					BatchSize:    64,
					Gamma:        0.99,
					GAELambda:    0.95,
				},
				DQN: DQNConfig{
					Enabled:          true,
					LearningRate:     0.001,
					Gamma:            0.99,
					EpsilonStart:     0.9,
					EpsilonEnd:       0.05,
					EpsilonDecay:     0.995,
					TargetUpdateFreq: 500,
					BufferSize:       10000,
					ReplanWeight:     0.3,
				},
				Reward: RewardConfig{
					TaskSuccessWeight:      0.5,
					EfficiencyWeight:       0.3,
					SafetyWeight:           0.15,
					UserSatisfactionWeight: 0.05,
				},
			},
			Compression: CompressionConfig{
				Strategy: "layered",
				Layers: CompressionLayers{
					ToolEvictionPct: 30,
					SummarizePct:    50,
					SlimPromptPct:   70,
					EmergencyPct:    90,
				},
				TokenEstimateRatio: 0.25,
			},
			SpeculativeExecution: SpeculativeExecutionConfig{
				Enabled:     true,
				MaxInFlight: 3,
			},
			Team: TeamConfig{
				Enabled:    true,
				MaxWorkers: 3,
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
			Addr: ":8080",
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
			Browser: BrowserToolConfig{
				Enabled: true,
				Timeout: 30 * time.Second,
			},
			Verify: VerifyConfig{
				Enabled: true,
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
		Knowledge: KnowledgeConfig{
			Enabled:      true,
			ChunkSize:    512,
			ChunkOverlap: 64,
			BM25Weight:   0.4,
			VectorWeight: 0.6,
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
		RateLimit: RateLimitConfig{
			Enabled:        false,
			RequestsPerSec: 10,
			Burst:          20,
		},
		Evolution: evolution.DefaultConfig(),
	}
}
