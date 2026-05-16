package config

import "time"

// SandboxConfig configures the security sandbox system.
type SandboxConfig struct {
	Enabled             bool              `yaml:"enabled"`
	AllowedDirectories  []string          `yaml:"allowed_directories"`
	ReadonlyDirectories []string          `yaml:"readonly_directories"`
	Bash                BashSandboxConfig `yaml:"bash"`
	Network             NetworkConfig     `yaml:"network"`
}

// BashSandboxConfig configures bash tool execution backend.
type BashSandboxConfig struct {
	Backend string              `yaml:"backend"` // "docker" | "host"
	Docker  DockerSandboxConfig `yaml:"docker"`
}

// DockerSandboxConfig configures the Docker session container.
type DockerSandboxConfig struct {
	Image       string        `yaml:"image"`
	Network     string        `yaml:"network"` // "none" | "bridge" | "host"
	MemoryLimit string        `yaml:"memory_limit"`
	CPULimit    string        `yaml:"cpu_limit"`
	IdleTimeout time.Duration `yaml:"idle_timeout"`
}

// NetworkConfig configures network access policy for HTTP tools.
type NetworkConfig struct {
	Mode      string   `yaml:"mode"` // "none" | "blacklist" | "whitelist"
	Blacklist []string `yaml:"blacklist"`
	Whitelist []string `yaml:"whitelist"`
}

// RateLimitConfig configures the rate limiting and backpressure system.
type RateLimitConfig struct {
	Enabled        bool    `yaml:"enabled"`
	RequestsPerSec float64 `yaml:"requests_per_sec"` // default: 10
	Burst          int     `yaml:"burst"`            // default: 20
}

// PermissionsConfig configures the permission engine.
type PermissionsConfig struct {
	Default string           `yaml:"default"` // "none", "notify", "approve", "deny" (default: "approve"; legacy "allow"/"ask" accepted)
	Rules   []PermissionRule `yaml:"rules"`
}

// PermissionRule defines a single permission rule.
type PermissionRule struct {
	Tool        string `yaml:"tool"`         // tool name or "*" wildcard
	Pattern     string `yaml:"pattern"`      // glob pattern for command/input
	PathPattern string `yaml:"path_pattern"` // glob pattern for file paths
	Action      string `yaml:"action"`       // "none", "notify", "approve", "deny" (legacy "allow"/"ask" accepted)
}
