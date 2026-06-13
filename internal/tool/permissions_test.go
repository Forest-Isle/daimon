package tool

import (
	"context"
	"strings"
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
		{"git auto-allowed", "bash", `{"command":"git commit -m fix"}`, ToolCapabilities{}, PermissionNone},
		{"rm denied", "bash", `{"command":"rm -rf /home"}`, ToolCapabilities{}, PermissionDeny},
		{"etc denied", "file", `{"path":"/etc/passwd","action":"write"}`, ToolCapabilities{}, PermissionDeny},
		{"other approved", "http", `{}`, ToolCapabilities{}, PermissionApprove},
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

func TestPermissionEngineDestructiveDefault(t *testing.T) {
	// No rules, but tool is destructive — should default to approve
	pe := NewPermissionEngine(nil, "allow", nil)
	result := pe.Evaluate("custom_tool", `{}`, ToolCapabilities{IsDestructive: true})
	if result.Action != PermissionApprove {
		t.Errorf("destructive tool should default to approve, got %v", result.Action)
	}
}

func TestPermissionAction_NoneAndNotify(t *testing.T) {
	rules := []PermissionRule{
		{Tool: "file_read", Action: "none"},
		{Tool: "bash", Pattern: "ls *", Action: "notify"},
		{Tool: "bash", Pattern: "rm *", Action: "approve"},
	}
	pe := NewPermissionEngine(rules, "approve", nil)

	r := pe.Evaluate("file_read", `{"path":"/tmp/test"}`, ToolCapabilities{})
	if r.Action != PermissionNone {
		t.Errorf("file_read: got %v, want none", r.Action)
	}

	r = pe.Evaluate("bash", `{"command":"ls -la"}`, ToolCapabilities{})
	if r.Action != PermissionNotify {
		t.Errorf("bash ls: got %v, want notify", r.Action)
	}

	r = pe.Evaluate("bash", `{"command":"rm -rf /tmp/foo"}`, ToolCapabilities{})
	if r.Action != PermissionApprove {
		t.Errorf("bash rm: got %v, want approve", r.Action)
	}
}

func TestPermissionAction_BackwardCompat(t *testing.T) {
	rules := []PermissionRule{
		{Tool: "bash", Action: "allow"},
		{Tool: "http", Action: "ask"},
	}
	pe := NewPermissionEngine(rules, "ask", nil)

	r := pe.Evaluate("bash", `{"command":"echo hi"}`, ToolCapabilities{})
	if r.Action != PermissionNone {
		t.Errorf("allow should map to none, got %v", r.Action)
	}

	r = pe.Evaluate("http", `{"url":"http://example.com"}`, ToolCapabilities{})
	if r.Action != PermissionApprove {
		t.Errorf("ask should map to approve, got %v", r.Action)
	}
}

func TestPermissionProfileRemotePromotesWriteAllowToApprove(t *testing.T) {
	pe := NewPermissionEngine([]PermissionRule{
		{Tool: "file_write", Action: "none"},
	}, "none", nil)

	ctx := WithChannelClass(context.Background(), ToolChannelRemote)
	r := pe.EvaluateWithContext(ctx, "file_write", `{"path":"x","content":"y"}`, ToolCapabilities{
		IsReadOnly: false,
	})
	if r.Action != PermissionApprove {
		t.Fatalf("remote write action = %v, want approve", r.Action)
	}
	if !strings.Contains(r.Reason, "profile_requires_approval_for_write") {
		t.Fatalf("reason = %q, want profile write floor", r.Reason)
	}
}

func TestPermissionProfileScheduledPromotesDestructiveAllowToApprove(t *testing.T) {
	pe := NewPermissionEngine([]PermissionRule{
		{Tool: "bash", Pattern: "git *", Action: "none"},
	}, "none", nil)

	ctx := WithChannelClass(context.Background(), ToolChannelScheduled)
	r := pe.EvaluateWithContext(ctx, "bash", `{"command":"git commit -m ok"}`, ToolCapabilities{
		IsDestructive: true,
	})
	if r.Action != PermissionApprove {
		t.Fatalf("scheduled destructive action = %v, want approve", r.Action)
	}
	if !strings.Contains(r.Reason, "profile_requires_approval_for_destructive") {
		t.Fatalf("reason = %q, want destructive profile floor", r.Reason)
	}
}

func TestPermissionProfileLocalPreservesExplicitAllow(t *testing.T) {
	pe := NewPermissionEngine([]PermissionRule{
		{Tool: "bash", Pattern: "git *", Action: "none"},
	}, "approve", nil)

	r := pe.Evaluate("bash", `{"command":"git status"}`, ToolCapabilities{IsDestructive: true})
	if r.Action != PermissionNone {
		t.Fatalf("local explicit allow action = %v, want none", r.Action)
	}
}

// TestPermissionInternalReadOnlyAllowedOnLegacyPath is the regression for the
// autonomous-episode read bug: with no rules + a legacy Policy + a default of
// "approve" (the empty-string default), the legacy path would return approve for
// every tool, and an internal episode — having no channel — could never read its
// own world. A pure read must be allowed regardless of that path.
func TestPermissionInternalReadOnlyAllowedOnLegacyPath(t *testing.T) {
	pe := NewPermissionEngine(nil, "", NewPolicy(nil)) // defaultAct = approve, legacy active

	ctx := WithChannelClass(context.Background(), ToolChannelInternal)
	r := pe.EvaluateWithContext(ctx, "world_read", `{}`, ToolCapabilities{IsReadOnly: true})
	if r.Action != PermissionNone {
		t.Fatalf("internal read-only action = %v, want none (reason: %s)", r.Action, r.Reason)
	}
	if r.Reason != "internal_readonly" {
		t.Fatalf("reason = %q, want internal_readonly", r.Reason)
	}
}

func TestPermissionInternalWriteStillEscalates(t *testing.T) {
	pe := NewPermissionEngine(nil, "none", nil) // default allow — only the floor should gate

	ctx := WithChannelClass(context.Background(), ToolChannelInternal)
	r := pe.EvaluateWithContext(ctx, "world_edit", `{}`, ToolCapabilities{IsReadOnly: false})
	if r.Action != PermissionApprove {
		t.Fatalf("internal write action = %v, want approve", r.Action)
	}
}

// TestPermissionInternalReadWithNetworkEscalates guards the carve-out: a
// read-only tool that still reaches the network (e.g. a web fetch) must not slip
// past the internal network floor.
func TestPermissionInternalReadWithNetworkEscalates(t *testing.T) {
	pe := NewPermissionEngine(nil, "none", nil)

	ctx := WithChannelClass(context.Background(), ToolChannelInternal)
	r := pe.EvaluateWithContext(ctx, "web_fetch", `{}`, ToolCapabilities{IsReadOnly: true, RequiresNetwork: true})
	if r.Action != PermissionApprove {
		t.Fatalf("internal read+network action = %v, want approve", r.Action)
	}
}
