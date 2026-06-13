package config

import "time"

type ToolsConfig struct {
	Bash                BashToolConfig            `yaml:"bash"`
	File                FileToolConfig            `yaml:"file"`
	HTTP                HTTPToolConfig            `yaml:"http"`
	Exec                ExecConfig                `yaml:"exec"`
	Verify              VerifyConfig              `yaml:"verify"`
	MCP                 MCPConfig                 `yaml:"mcp"`
	ConcurrentExecution ConcurrentExecutionConfig `yaml:"concurrent_execution"`
	ResultPersistence   ResultPersistenceConfig   `yaml:"result_persistence"`
}

// ExecConfig controls the shell execution backend for the bash tool. Backend
// "host" (default) runs commands directly; "seatbelt" runs every command under
// the macOS sandbox. Regardless of this default, commands triggered by a
// non-local source (remote/scheduled/internal/background) are always sandboxed.
type ExecConfig struct {
	Backend string `yaml:"backend"` // "host" (default) | "seatbelt"
}

type VerifyConfig struct {
	Enabled bool `yaml:"enabled"`
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
	Servers      map[string]MCPServerConfig `yaml:"servers"`
	PollInterval time.Duration              `yaml:"poll_interval"` // directory scan interval; default: 30s
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
