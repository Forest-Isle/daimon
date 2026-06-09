package agent

import (
	"fmt"
	"time"
)

// duration is a custom type for YAML duration parsing.
type duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler for duration.
func (d *duration) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	if s == "" {
		*d = duration(120 * time.Second) // default
		return nil
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = duration(parsed)
	return nil
}

// Duration returns the time.Duration value.
func (d duration) Duration() time.Duration {
	return time.Duration(d)
}

// ExecutionMode determines how a sub-agent is launched.
type ExecutionMode string

const (
	// ExecModeSpawn creates an independent Runtime (default, current behavior).
	ExecModeSpawn ExecutionMode = "spawn"
	// ExecModeFork inherits the parent Runtime's full session context.
	ExecModeFork ExecutionMode = "fork"
	// ExecModeBackground runs asynchronously in a goroutine.
	ExecModeBackground ExecutionMode = "background"
)

// PermissionMode controls how a sub-agent handles tool permission checks.
type PermissionMode string

const (
	// PermModeDefault follows the parent's permission behavior.
	PermModeDefault PermissionMode = ""
	// PermModeBubble sends permission requests to the parent Runtime.
	PermModeBubble PermissionMode = "bubble"
	// PermModeAcceptEdits auto-approves read/write, bubbles dangerous ops.
	PermModeAcceptEdits PermissionMode = "accept_edits"
	// PermModeBypass skips all permission checks (use for trusted read-only agents).
	PermModeBypass PermissionMode = "bypass"
)

// FailureStrategy controls how parallel sub-agent execution handles failures.
type FailureStrategy string

const (
	StrategyBestEffort FailureStrategy = "best_effort"
	StrategyFailFast   FailureStrategy = "fail_fast"
)

// AgentSpec defines a specialized sub-agent that can be registered as a tool.
type AgentSpec struct {
	Name             string   `yaml:"name"`
	Description      string   `yaml:"description"`
	SystemPrompt     string   `yaml:"system_prompt"`
	Model            string   `yaml:"model"`             // optional LLM model override
	MaxTokens        int      `yaml:"max_tokens"`        // optional max_tokens override
	MaxIterations    int      `yaml:"max_iterations"`    // default 5
	Tools            []string `yaml:"tools"`             // tool whitelist (empty = all, agent_* always excluded)
	Tags             []string `yaml:"tags"`              // routing tags for semantic matching
	Mode             string   `yaml:"mode"`              // "simple" (default) | "cognitive"
	Timeout          duration `yaml:"timeout"`           // execution timeout, default 120s
	RequiresApproval bool     `yaml:"requires_approval"` // require user approval before execution
	MaxRetries       int      `yaml:"max_retries"`       // retry count on failure, default 0

	ExecutionMode   ExecutionMode   `yaml:"execution_mode"`    // "spawn" (default) | "fork" | "background"
	PermissionMode  PermissionMode  `yaml:"permission_mode"`   // "" | "bubble" | "accept_edits" | "bypass"
	FailureStrategy FailureStrategy `yaml:"failure_strategy"`  // "best_effort" (default) | "fail_fast"
	InheritContext  bool            `yaml:"inherit_context"`   // fork mode: inherit parent context
	MaxOutputTokens int             `yaml:"max_output_tokens"` // limit output tokens (0 = no limit)

	Backend    BackendType      `yaml:"backend"`     // "in_process" (default) | "subprocess" | "docker"
	Hooks      AgentHookConfig  `yaml:"hooks"`       // lifecycle hooks
	MCPServers []AgentMCPConfig `yaml:"mcp_servers"` // per-agent MCP servers
}

// DefaultMaxIterations is the default iteration limit for sub-agents.
const DefaultMaxIterations = 5

// Validate checks that the spec has required fields and applies defaults.
func (s *AgentSpec) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("agent spec: name is required")
	}
	if s.Description == "" {
		return fmt.Errorf("agent spec: description is required for %q", s.Name)
	}
	if s.MaxIterations <= 0 {
		s.MaxIterations = DefaultMaxIterations
	}
	if s.Mode == "" {
		s.Mode = "simple"
	}
	if s.Timeout == 0 {
		s.Timeout = duration(120 * time.Second)
	}
	if s.ExecutionMode == "" {
		s.ExecutionMode = ExecModeSpawn
	}
	switch s.ExecutionMode {
	case ExecModeSpawn, ExecModeFork, ExecModeBackground:
		// valid
	default:
		return fmt.Errorf("agent spec %q: invalid execution_mode %q", s.Name, s.ExecutionMode)
	}
	switch s.PermissionMode {
	case PermModeDefault, PermModeBubble, PermModeAcceptEdits, PermModeBypass:
		// valid
	default:
		return fmt.Errorf("agent spec %q: invalid permission_mode %q", s.Name, s.PermissionMode)
	}
	if s.FailureStrategy == "" {
		s.FailureStrategy = StrategyBestEffort
	}
	switch s.FailureStrategy {
	case StrategyBestEffort, StrategyFailFast:
		// valid
	default:
		return fmt.Errorf("agent spec %q: invalid failure_strategy %q", s.Name, s.FailureStrategy)
	}
	switch s.Backend {
	case "", BackendInProcess, BackendSubprocess, BackendDocker:
		// valid
	default:
		return fmt.Errorf("agent spec %q: invalid backend %q", s.Name, s.Backend)
	}
	if s.ExecutionMode == ExecModeFork {
		s.InheritContext = true // fork always inherits
	}
	return nil
}
