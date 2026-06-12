package config

// PermissionsConfig configures the permission engine.
type PermissionsConfig struct {
	Default  string                             `yaml:"default"` // "none", "notify", "approve", "deny" (default: "approve"; legacy "allow"/"ask" accepted)
	Rules    []PermissionRule                   `yaml:"rules"`
	Profiles map[string]PermissionProfileConfig `yaml:"profiles"`
}

// PermissionRule defines a single permission rule.
type PermissionRule struct {
	Tool        string `yaml:"tool"`         // tool name or "*" wildcard
	Pattern     string `yaml:"pattern"`      // glob pattern for command/input
	PathPattern string `yaml:"path_pattern"` // glob pattern for file paths
	Action      string `yaml:"action"`       // "none", "notify", "approve", "deny" (legacy "allow"/"ask" accepted)
}

// PermissionProfileConfig adjusts defaults for a channel class such as local,
// remote, scheduled, or background.
type PermissionProfileConfig struct {
	Default                       string `yaml:"default"`
	RequireApprovalForWrite       *bool  `yaml:"require_approval_for_write"`
	RequireApprovalForDestructive *bool  `yaml:"require_approval_for_destructive"`
	RequireApprovalForNetwork     *bool  `yaml:"require_approval_for_network"`
}
