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
	LLM       LLMConfig       `yaml:"llm"`
	Telegram  TelegramConfig  `yaml:"telegram"`
	TUI       TUIConfig       `yaml:"tui"`
	Agent     AgentConfig     `yaml:"agent"`
	Store     StoreConfig     `yaml:"store"`
	Memory    MemoryConfig    `yaml:"memory"`
	Knowledge KnowledgeConfig `yaml:"knowledge"` // Phase 2 placeholder
	Graph     GraphConfig     `yaml:"graph"`     // Phase 3 placeholder
	Scheduler SchedulerConfig `yaml:"scheduler"`
	Tools     ToolsConfig     `yaml:"tools"`
	Server    ServerConfig    `yaml:"server"`
	Log       LogConfig       `yaml:"log"`
	Skills      SkillsConfig      `yaml:"skills"`
	Agents      AgentsConfig      `yaml:"agents"`
	Permissions PermissionsConfig    `yaml:"permissions"`
	Hooks       HooksConfig          `yaml:"hooks"`
	Evolution   evolution.Config     `yaml:"evolution"`
}

// HooksConfig configures the hook event system.
type HooksConfig struct {
	PreToolUse    []HookHandlerConfig `yaml:"pre_tool_use"`
	PostToolUse   []HookHandlerConfig `yaml:"post_tool_use"`
	OnUserMessage []HookHandlerConfig `yaml:"on_user_message"`
	PreCompact    []HookHandlerConfig `yaml:"pre_compact"`
}

// HookHandlerConfig configures a single hook handler.
type HookHandlerConfig struct {
	Type   string         `yaml:"type"`
	Config map[string]any `yaml:"config"`
}

// PermissionsConfig configures the permission engine.
type PermissionsConfig struct {
	Default string           `yaml:"default"` // "allow", "deny", "ask" (default: "ask")
	Rules   []PermissionRule `yaml:"rules"`
}

// PermissionRule defines a single permission rule.
type PermissionRule struct {
	Tool        string `yaml:"tool"`         // tool name or "*" wildcard
	Pattern     string `yaml:"pattern"`      // glob pattern for command/input
	PathPattern string `yaml:"path_pattern"` // glob pattern for file paths
	Action      string `yaml:"action"`       // "allow", "deny", "ask"
}

// TUIConfig configures the TUI (terminal UI) channel.
type TUIConfig struct {
	AutoApprove bool   `yaml:"auto_approve"` // skip approval prompts
	Theme       string `yaml:"theme"`        // reserved for future use
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
	Token          string  `yaml:"token"`
	AllowedUserIDs []int64 `yaml:"allowed_user_ids"`
}

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

type StoreConfig struct {
	Path string `yaml:"path"`
}

type MemoryConfig struct {
	Enabled               bool          `yaml:"enabled"`
	StorageType           string        `yaml:"storage_type"`           // "file" or "sqlite" (default: "file")
	StorageDir            string        `yaml:"storage_dir"`            // directory for file-based storage (default: ~/.IronClaw/memory)
	EmbeddingModel        string        `yaml:"embedding_model"`
	OpenAIAPIKey          string        `yaml:"openai_api_key"`
	FactExtraction        bool          `yaml:"fact_extraction"`        // enable LLM fact extraction
	SimilarityThreshold   float64       `yaml:"similarity_threshold"`   // dedup threshold (default 0.85)
	ConsolidationInterval time.Duration `yaml:"consolidation_interval"` // session->user promotion interval
	BM25Weight            float64       `yaml:"bm25_weight"`            // BM25 weight in RRF (default 0.4)
	VectorWeight          float64       `yaml:"vector_weight"`          // vector weight in RRF (default 0.6)
	EnableVSS             bool          `yaml:"enable_vss"`             // enable HNSW indexing via sqlite-vss
	VectorDimension       int           `yaml:"vector_dimension"`       // embedding dimension (default: 1536)
	EnableSearchCache     bool          `yaml:"enable_search_cache"`    // enable search result caching
	SearchCacheSize       int           `yaml:"search_cache_size"`      // max cached queries (default: 500)
	SearchCacheTTL        time.Duration `yaml:"search_cache_ttl"`       // cache TTL (default: 5min)
	FileStorage           FileStorageConfig `yaml:"file_storage"`       // file storage specific settings
	ReflectionCountThreshold int           `yaml:"reflection_count_threshold"` // default 10
	ReflectionDriftThreshold float64       `yaml:"reflection_drift_threshold"` // default 0.7
	ReflectionL2Trigger      int           `yaml:"reflection_l2_trigger"`      // default 5
	RetentionEpisodic        time.Duration `yaml:"retention_episodic"`         // e.g., "720h" for 30 days
	RetentionSemantic        time.Duration `yaml:"retention_semantic"`         // e.g., "8760h" for 365 days
	RetentionProcedural      time.Duration `yaml:"retention_procedural"`       // 0 = never
}

// FileStorageConfig holds file-based storage specific settings.
type FileStorageConfig struct {
	FlushInterval   time.Duration `yaml:"flush_interval"`   // transaction log flush interval (default: 5s)
	ChunkThreshold  int           `yaml:"chunk_threshold"`  // facts per file before chunking (default: 200)
	Compression     bool          `yaml:"compression"`      // enable gzip compression for large files
}

// KnowledgeConfig holds configuration for the Phase 2 knowledge base package.
type KnowledgeConfig struct {
	Enabled           bool           `yaml:"enabled"`
	ChunkSize         int            `yaml:"chunk_size"`
	ChunkOverlap      int            `yaml:"chunk_overlap"`
	BM25Weight        float64        `yaml:"bm25_weight"`
	VectorWeight      float64        `yaml:"vector_weight"`
	GraphEnabled      bool           `yaml:"graph_enabled"`
	IngestDirs        []string       `yaml:"ingest_dirs"`
	Reranker          RerankerConfig `yaml:"reranker"`
	EnableSearchCache bool           `yaml:"enable_search_cache"` // enable search result caching
	SearchCacheSize   int            `yaml:"search_cache_size"`   // max cached queries (default: 500)
	SearchCacheTTL    time.Duration  `yaml:"search_cache_ttl"`    // cache TTL (default: 5min)
}

// RerankerConfig configures the optional LLM-based reranker.
type RerankerConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Provider string `yaml:"provider"` // "llm" or "none"
}

// GraphConfig holds configuration for the Phase 3 knowledge graph.
type GraphConfig struct {
	Enabled bool `yaml:"enabled"`
}

type SchedulerConfig struct {
	Enabled      bool          `yaml:"enabled"`
	PollInterval time.Duration `yaml:"poll_interval"`
}

type ToolsConfig struct {
	Bash                BashToolConfig            `yaml:"bash"`
	File                FileToolConfig            `yaml:"file"`
	HTTP                HTTPToolConfig            `yaml:"http"`
	Browser             BrowserToolConfig         `yaml:"browser"`
	MCP                 MCPConfig                 `yaml:"mcp"`
	ConcurrentExecution ConcurrentExecutionConfig `yaml:"concurrent_execution"`
	ResultPersistence   ResultPersistenceConfig   `yaml:"result_persistence"`
}

// ConcurrentExecutionConfig controls parallel execution of read-only tools.
type ConcurrentExecutionConfig struct {
	Enabled        bool `yaml:"enabled"`
	MaxConcurrency int  `yaml:"max_concurrency"`
}

// ResultPersistenceConfig controls disk persistence of large tool results.
type ResultPersistenceConfig struct {
	Enabled        bool   `yaml:"enabled"`
	ThresholdBytes int    `yaml:"threshold_bytes"`
	PreviewChars   int    `yaml:"preview_chars"`
	CacheDir       string `yaml:"cache_dir"`
	TTLHours       int    `yaml:"ttl_hours"`
}

type MCPConfig struct {
	Servers map[string]MCPServerConfig `yaml:"servers"`
}

type MCPServerConfig struct {
	Command          string            `yaml:"command"`
	Args             []string          `yaml:"args"`
	Env              map[string]string `yaml:"env"`
	RequiresApproval bool              `yaml:"requires_approval"`
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

// BrowserToolConfig holds configuration for the browser (HTTP GET) tool.
type BrowserToolConfig struct {
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
		Scheduler: SchedulerConfig{
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
		Knowledge: KnowledgeConfig{
			ChunkSize:    512,
			ChunkOverlap: 64,
			BM25Weight:   0.4,
			VectorWeight: 0.6,
		},
		Permissions: PermissionsConfig{
			Default: "ask",
		},
		Evolution: evolution.DefaultConfig(),
	}
}
