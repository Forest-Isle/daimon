package tool

import (
	"testing"
)

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern  string
		value    string
		expected bool
	}{
		{"git *", "git commit -m fix", true},
		{"git *", "git", false},
		{"rm -rf *", "rm -rf /home", true},
		{"rm -rf *", "rm file.txt", false},
		{"*", "anything", true},
		{"docker *", "docker build .", true},
		{"docker *", "podman build", false},
		{"", "", true},
		{"*.txt", "file.txt", true},
		{"*.txt", "file.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.value, func(t *testing.T) {
			got := matchGlob(tt.pattern, tt.value)
			if got != tt.expected {
				t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.value, got, tt.expected)
			}
		})
	}
}

func TestPermissionEngineEvaluate(t *testing.T) {
	rules := []PermissionRule{
		{Tool: "bash", Pattern: "git *", Action: "allow"},
		{Tool: "bash", Pattern: "rm -rf *", Action: "deny"},
		{Tool: "file", PathPattern: "/etc/*", Action: "deny"},
		{Tool: "*", Action: "ask"},
	}

	pe := NewPermissionEngine(rules, "ask", nil)

	tests := []struct {
		name     string
		toolName string
		input    string
		caps     ToolCapabilities
		expected PermissionAction
	}{
		{"git allowed", "bash", `{"command":"git commit -m fix"}`, ToolCapabilities{}, PermissionAllow},
		{"rm denied", "bash", `{"command":"rm -rf /home"}`, ToolCapabilities{}, PermissionDeny},
		{"etc denied", "file", `{"path":"/etc/passwd","action":"write"}`, ToolCapabilities{}, PermissionDeny},
		{"other asks", "http", `{}`, ToolCapabilities{}, PermissionAsk},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pe.Evaluate(tt.toolName, tt.input, tt.caps)
			if result.Action != tt.expected {
				t.Errorf("Evaluate() action = %v, want %v (reason: %s)", result.Action, tt.expected, result.Reason)
			}
		})
	}
}

func TestPermissionEngineLegacyFallback(t *testing.T) {
	// No rules configured — should use legacy Policy
	policy := NewPolicy([]string{"rm -rf"})
	pe := NewPermissionEngine(nil, "ask", policy)

	result := pe.Evaluate("bash", `{"command":"rm -rf /"}`, ToolCapabilities{})
	if result.Action != PermissionDeny {
		t.Errorf("legacy fallback should deny rm -rf, got %v", result.Action)
	}
	if result.Reason != "legacy_policy" {
		t.Errorf("reason should be legacy_policy, got %s", result.Reason)
	}
}

func TestMergeRules(t *testing.T) {
	project := []PermissionRule{{Tool: "bash", Pattern: "docker build *", Action: "allow"}}
	global := []PermissionRule{{Tool: "bash", Pattern: "docker *", Action: "deny"}}

	merged := MergeRules(project, global)
	if len(merged) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(merged))
	}
	// Project rules should come first
	if merged[0].Action != "allow" {
		t.Error("project rule should be first")
	}
	if merged[1].Action != "deny" {
		t.Error("global rule should be second")
	}
}

func TestPermissionEngineDestructiveDefault(t *testing.T) {
	// No rules, but tool is destructive — should default to ask
	pe := NewPermissionEngine(nil, "allow", nil)
	result := pe.Evaluate("custom_tool", `{}`, ToolCapabilities{IsDestructive: true})
	if result.Action != PermissionAsk {
		t.Errorf("destructive tool should default to ask, got %v", result.Action)
	}
}
