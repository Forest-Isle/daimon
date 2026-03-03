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

// AgentSpec defines a specialized sub-agent that can be registered as a tool.
type AgentSpec struct {
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	SystemPrompt  string   `yaml:"system_prompt"`
	Model         string   `yaml:"model"`           // optional LLM model override
	MaxTokens     int      `yaml:"max_tokens"`      // optional max_tokens override
	MaxIterations int      `yaml:"max_iterations"`  // default 5
	Tools         []string `yaml:"tools"`           // tool whitelist (empty = all, agent_* always excluded)
	Tags          []string `yaml:"tags"`            // routing tags for semantic matching
	Mode          string   `yaml:"mode"`            // "simple" (default) | "cognitive"
	Timeout       duration `yaml:"timeout"`         // execution timeout, default 120s
	RequiresApproval bool  `yaml:"requires_approval"` // require user approval before execution
	MaxRetries    int      `yaml:"max_retries"`     // retry count on failure, default 0

	// Phase 3: A2A remote agent support (reserved, not implemented)
	Remote *RemoteAgentConfig `yaml:"remote,omitempty"`
}

// RemoteAgentConfig holds configuration for A2A protocol remote agents.
// Phase 3 placeholder — when spec.Remote != nil, AgentTool will call a remote
// endpoint via A2A instead of creating a local Runtime.
type RemoteAgentConfig struct {
	URL       string            `yaml:"url"`        // A2A agent endpoint
	AgentCard string            `yaml:"agent_card"` // Agent Card URL
	AuthType  string            `yaml:"auth_type"`  // bearer/api_key/oauth
	AuthToken string            `yaml:"auth_token"`
	Timeout   time.Duration     `yaml:"timeout"`
	Headers   map[string]string `yaml:"headers"`
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
	return nil
}
