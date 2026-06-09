package config

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
