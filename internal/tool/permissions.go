package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// PermissionAction represents the result of a permission check.
type PermissionAction string

const (
	PermissionNone    PermissionAction = "none"
	PermissionNotify  PermissionAction = "notify"
	PermissionApprove PermissionAction = "approve"
	PermissionDeny    PermissionAction = "deny"
)

// parseAction normalizes a permission action string, accepting both new and legacy names.
func parseAction(s string) PermissionAction {
	switch s {
	case "none", "allow":
		return PermissionNone
	case "notify":
		return PermissionNotify
	case "approve", "ask":
		return PermissionApprove
	case "deny":
		return PermissionDeny
	default:
		return PermissionApprove
	}
}

func ParsePermissionAction(s string) PermissionAction {
	return parseAction(s)
}

// PermissionRule defines a single permission rule for the engine.
type PermissionRule struct {
	Tool        string `yaml:"tool"`
	Pattern     string `yaml:"pattern"`
	PathPattern string `yaml:"path_pattern"`
	Action      string `yaml:"action"`
}

// PermissionResult is the outcome of evaluating permissions for a tool call.
type PermissionResult struct {
	Action      PermissionAction
	MatchedRule *PermissionRule // nil if default action
	Reason      string
}

type PermissionProfile struct {
	DefaultAction                 PermissionAction
	RequireApprovalForWrite       bool
	RequireApprovalForDestructive bool
	RequireApprovalForNetwork     bool
}

// PermissionEngine evaluates tool execution requests against configured rules.
type PermissionEngine struct {
	rules      []PermissionRule
	defaultAct PermissionAction
	profiles   map[ToolChannelClass]PermissionProfile
	legacy     *Policy // fallback for backward compatibility
}

// NewPermissionEngine creates a permission engine from rules and a default action.
// If no rules are provided and a legacy Policy is given, it falls back to legacy behavior.
func NewPermissionEngine(rules []PermissionRule, defaultAction string, legacy *Policy) *PermissionEngine {
	defaultAct := parseAction(defaultAction)
	return &PermissionEngine{
		rules:      rules,
		defaultAct: defaultAct,
		profiles:   DefaultPermissionProfiles(defaultAct),
		legacy:     legacy,
	}
}

func NewPermissionEngineWithProfiles(rules []PermissionRule, defaultAction string, legacy *Policy, profiles map[ToolChannelClass]PermissionProfile) *PermissionEngine {
	pe := NewPermissionEngine(rules, defaultAction, legacy)
	for class, profile := range profiles {
		pe.profiles[class] = profile
	}
	return pe
}

func DefaultPermissionProfiles(defaultAct PermissionAction) map[ToolChannelClass]PermissionProfile {
	return map[ToolChannelClass]PermissionProfile{
		ToolChannelLocal: {
			DefaultAction: defaultAct,
		},
		ToolChannelRemote: {
			DefaultAction:                 defaultAct,
			RequireApprovalForWrite:       true,
			RequireApprovalForDestructive: true,
			RequireApprovalForNetwork:     true,
		},
		ToolChannelScheduled: {
			DefaultAction:                 defaultAct,
			RequireApprovalForWrite:       true,
			RequireApprovalForDestructive: true,
			RequireApprovalForNetwork:     true,
		},
		ToolChannelBackground: {
			DefaultAction:                 defaultAct,
			RequireApprovalForWrite:       true,
			RequireApprovalForDestructive: true,
			RequireApprovalForNetwork:     true,
		},
	}
}

// Evaluate checks whether a tool call should be allowed, denied, or requires approval.
func (pe *PermissionEngine) Evaluate(toolName, input string, caps ToolCapabilities) PermissionResult {
	return pe.evaluate(ToolChannelLocal, toolName, input, caps)
}

func (pe *PermissionEngine) EvaluateWithContext(ctx context.Context, toolName, input string, caps ToolCapabilities) PermissionResult {
	return pe.evaluate(ChannelClassFromContext(ctx), toolName, input, caps)
}

func (pe *PermissionEngine) evaluate(channelClass ToolChannelClass, toolName, input string, caps ToolCapabilities) PermissionResult {
	// If no rules configured, fall back to legacy behavior
	if len(pe.rules) == 0 && pe.legacy != nil {
		return pe.applyProfileFloor(channelClass, pe.evaluateLegacy(toolName, input, caps), caps)
	}

	// Extract command/path from input for pattern matching
	command := extractCommand(toolName, input)
	filePath := extractFilePath(toolName, input)

	// Evaluate rules top-to-bottom, first match wins
	for i := range pe.rules {
		rule := &pe.rules[i]
		if !matchToolPattern(rule.Tool, toolName) {
			continue
		}

		// Check command pattern
		if rule.Pattern != "" && !matchGlob(rule.Pattern, command) {
			continue
		}

		// Check path pattern
		if rule.PathPattern != "" && !matchGlob(rule.PathPattern, filePath) {
			continue
		}

		return pe.applyProfileFloor(channelClass, PermissionResult{
			Action:      parseAction(rule.Action),
			MatchedRule: rule,
			Reason:      "rule_match",
		}, caps)
	}

	// No rule matched — check capabilities for destructive tools
	if caps.IsDestructive {
		return pe.applyProfileFloor(channelClass, PermissionResult{
			Action: PermissionApprove,
			Reason: "capability_default_destructive",
		}, caps)
	}

	// Default action
	return pe.applyProfileFloor(channelClass, PermissionResult{
		Action: pe.profile(channelClass).DefaultAction,
		Reason: "default",
	}, caps)
}

func (pe *PermissionEngine) profile(class ToolChannelClass) PermissionProfile {
	if pe.profiles != nil {
		if p, ok := pe.profiles[class]; ok {
			if p.DefaultAction == "" {
				p.DefaultAction = pe.defaultAct
			}
			return p
		}
	}
	p := DefaultPermissionProfiles(pe.defaultAct)[class]
	if p.DefaultAction == "" {
		p.DefaultAction = pe.defaultAct
	}
	return p
}

func (pe *PermissionEngine) applyProfileFloor(class ToolChannelClass, result PermissionResult, caps ToolCapabilities) PermissionResult {
	if result.Action == PermissionDeny || result.Action == PermissionApprove {
		return result
	}
	profile := pe.profile(class)
	reason := ""
	switch {
	case profile.RequireApprovalForDestructive && caps.IsDestructive:
		reason = "profile_requires_approval_for_destructive"
	case profile.RequireApprovalForNetwork && caps.RequiresNetwork:
		reason = "profile_requires_approval_for_network"
	case profile.RequireApprovalForWrite && !caps.IsReadOnly:
		reason = "profile_requires_approval_for_write"
	}
	if reason == "" {
		return result
	}
	result.Action = PermissionApprove
	if class != "" {
		reason = fmt.Sprintf("%s:%s", reason, class)
	}
	if result.Reason != "" {
		result.Reason += "+" + reason
	} else {
		result.Reason = reason
	}
	return result
}

// evaluateLegacy uses the old Policy blocklist for backward compatibility.
func (pe *PermissionEngine) evaluateLegacy(toolName, input string, caps ToolCapabilities) PermissionResult {
	if toolName == "bash" && pe.legacy != nil {
		if msg := pe.legacy.CheckBashCommand(extractCommand(toolName, input)); msg != "" {
			return PermissionResult{
				Action: PermissionDeny,
				Reason: "legacy_policy",
			}
		}
	}
	// Legacy: use RequiresApproval behavior (handled by caller)
	return PermissionResult{
		Action: pe.defaultAct,
		Reason: "legacy_default",
	}
}

// matchToolPattern matches a tool name against a pattern.
// Supports "*" as wildcard for any tool.
func matchToolPattern(pattern, toolName string) bool {
	if pattern == "*" {
		return true
	}
	matched, _ := filepath.Match(pattern, toolName)
	return matched
}

// matchGlob matches a string against a glob pattern.
// For command-style patterns with spaces (e.g., "git *", "rm -rf *"),
// the pattern prefix is matched literally and the last glob segment
// matches the remainder of the value.
func matchGlob(pattern, value string) bool {
	if pattern == "" || value == "" {
		return pattern == "" && value == ""
	}
	// Try direct filepath.Match first (works for simple patterns like "*.txt")
	matched, _ := filepath.Match(pattern, value)
	if matched {
		return true
	}
	// Handle command-style patterns with spaces (e.g., "git *", "rm -rf *")
	if strings.Contains(pattern, " ") {
		// Find the last space-separated segment as the glob, rest is literal prefix
		lastSpace := strings.LastIndex(pattern, " ")
		prefix := pattern[:lastSpace]
		glob := pattern[lastSpace+1:]

		// Value must start with the prefix followed by a space
		if !strings.HasPrefix(value, prefix+" ") {
			return false
		}
		remainder := value[len(prefix)+1:]
		if remainder == "" {
			return false
		}
		if glob == "*" {
			return true
		}
		suffixMatch, _ := filepath.Match(glob, remainder)
		return suffixMatch
	}
	return false
}

// extractCommand extracts the command string from tool input.
func extractCommand(toolName, input string) string {
	if toolName == "bash" {
		var parsed struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(input), &parsed) == nil {
			return parsed.Command
		}
	}
	return input
}

// extractFilePath extracts the file path from tool input.
func extractFilePath(toolName, input string) string {
	if toolName == "file" || strings.HasPrefix(toolName, "file") {
		var parsed struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(input), &parsed) == nil {
			return parsed.Path
		}
	}
	return ""
}
