package config

import (
	"os"
	"path/filepath"
)

// MergePermissionRules merges two sets of permission rules with deny-first semantics.
// A deny rule at ANY level overrides an allow/ask rule for the same tool.
// For non-deny conflicts, the overlay (higher-priority) rule wins.
func MergePermissionRules(base, overlay []PermissionRule) []PermissionRule {
	// Index base rules by (tool, pattern) key
	type ruleKey struct {
		Tool    string
		Pattern string
	}
	byKey := make(map[ruleKey]PermissionRule)
	var order []ruleKey

	for _, r := range base {
		k := ruleKey{r.Tool, r.Pattern}
		if _, exists := byKey[k]; !exists {
			order = append(order, k)
		}
		byKey[k] = r
	}

	for _, r := range overlay {
		k := ruleKey{r.Tool, r.Pattern}
		existing, exists := byKey[k]
		if !exists {
			order = append(order, k)
			byKey[k] = r
			continue
		}

		// Deny-first: if either rule is deny, the result is deny
		if r.Action == "deny" || existing.Action == "deny" {
			r.Action = "deny"
		}
		byKey[k] = r
	}

	merged := make([]PermissionRule, 0, len(order))
	for _, k := range order {
		merged = append(merged, byKey[k])
	}
	return merged
}

// LoadProjectInstructions loads .ironclaw/IRONCLAW.md as project-level instructions.
// These are injected as user context (probabilistic compliance, not system prompt).
func LoadProjectInstructions(workDir string) string {
	paths := []string{
		filepath.Join(workDir, ".ironclaw", "IRONCLAW.md"),
		filepath.Join(workDir, "IRONCLAW.md"),
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err == nil {
			return string(data)
		}
	}
	return ""
}
