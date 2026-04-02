package agent

import (
	"context"
	"testing"
	"time"
)

func TestPermissionEvaluator_Bypass(t *testing.T) {
	pe := NewPermissionEvaluator(PermModeBypass, nil)

	allowed, reason := pe.Check(context.Background(), "bash", "rm -rf /")
	if !allowed {
		t.Error("bypass should allow everything")
	}
	if reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestPermissionEvaluator_AcceptEdits_Safe(t *testing.T) {
	pe := NewPermissionEvaluator(PermModeAcceptEdits, nil)

	allowed, _ := pe.Check(context.Background(), "file_write", `{"path": "/tmp/test.txt"}`)
	if !allowed {
		t.Error("accept_edits should auto-approve safe operations")
	}

	allowed, _ = pe.Check(context.Background(), "bash", `{"command": "ls -la"}`)
	if !allowed {
		t.Error("accept_edits should auto-approve safe bash commands")
	}
}

func TestPermissionEvaluator_AcceptEdits_Dangerous(t *testing.T) {
	pe := NewPermissionEvaluator(PermModeAcceptEdits, nil)

	allowed, reason := pe.Check(context.Background(), "bash", `{"command": "rm -rf /"}`)
	if allowed {
		t.Error("accept_edits should block dangerous operations without parent channel")
	}
	if reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestPermissionEvaluator_AcceptEdits_DangerousBubble(t *testing.T) {
	parentCh := make(chan PermissionRequest, 1)
	pe := NewPermissionEvaluator(PermModeAcceptEdits, parentCh)

	// Simulate parent approving in background
	go func() {
		req := <-parentCh
		req.ResponseCh <- PermissionResponse{Allowed: true, Reason: "parent approved"}
	}()

	allowed, reason := pe.Check(context.Background(), "bash", `{"command": "rm -rf /tmp/old"}`)
	if !allowed {
		t.Errorf("expected approval from parent, got denied: %s", reason)
	}
}

func TestPermissionEvaluator_Bubble(t *testing.T) {
	parentCh := make(chan PermissionRequest, 1)
	pe := NewPermissionEvaluator(PermModeBubble, parentCh)

	// Parent denies
	go func() {
		req := <-parentCh
		req.ResponseCh <- PermissionResponse{Allowed: false, Reason: "denied by parent"}
	}()

	allowed, reason := pe.Check(context.Background(), "file_write", `{"path": "/etc/passwd"}`)
	if allowed {
		t.Error("bubble should respect parent denial")
	}
	if reason != "denied by parent" {
		t.Errorf("expected 'denied by parent', got %q", reason)
	}
}

func TestPermissionEvaluator_Bubble_NoParent(t *testing.T) {
	pe := NewPermissionEvaluator(PermModeBubble, nil)

	allowed, _ := pe.Check(context.Background(), "bash", "ls")
	if allowed {
		t.Error("bubble without parent channel should deny")
	}
}

func TestPermissionEvaluator_Bubble_ContextCancelled(t *testing.T) {
	parentCh := make(chan PermissionRequest, 1)
	pe := NewPermissionEvaluator(PermModeBubble, parentCh)

	ctx, cancel := context.WithCancel(context.Background())
	// Don't read from parentCh — simulate parent being busy
	go func() {
		// Read the request but don't respond — then cancel
		<-parentCh
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	allowed, _ := pe.Check(ctx, "bash", "ls")
	if allowed {
		t.Error("should deny when context is cancelled")
	}
}

func TestPermissionEvaluator_Default(t *testing.T) {
	pe := NewPermissionEvaluator(PermModeDefault, nil)

	allowed, _ := pe.Check(context.Background(), "bash", "anything")
	if !allowed {
		t.Error("default mode should allow (defer to standard checks)")
	}
}

func TestIsDangerousOperation(t *testing.T) {
	tests := []struct {
		tool   string
		input  string
		danger bool
	}{
		{"bash", `{"command": "rm -rf /"}`, true},
		{"bash", `{"command": "chmod 777 /tmp"}`, true},
		{"bash", `{"command": "kill -9 1234"}`, true},
		{"bash", `{"command": "shutdown -h now"}`, true},
		{"bash", `{"command": "ls -la"}`, false},
		{"bash", `{"command": "cat file.txt"}`, false},
		{"bash", `{"command": "go build ./..."}`, false},
		{"file_write", `{"path": "/tmp/test"}`, false},  // not bash
		{"file_read", `rm -rf /`, false},                 // not bash
		{"bash", `{"command": "iptables -F"}`, true},
	}

	for _, tt := range tests {
		result := IsDangerousOperation(tt.tool, tt.input)
		if result != tt.danger {
			t.Errorf("IsDangerousOperation(%q, %q) = %v, want %v", tt.tool, tt.input, result, tt.danger)
		}
	}
}

func TestPermissionEvaluator_Mode(t *testing.T) {
	pe := NewPermissionEvaluator(PermModeBubble, nil)
	if pe.Mode() != PermModeBubble {
		t.Errorf("expected bubble, got %q", pe.Mode())
	}
}
