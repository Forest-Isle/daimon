package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// dangerousCommands is the blocklist for accept_edits mode.
// These commands are too dangerous to auto-approve even in accept_edits mode.
var dangerousCommands = []string{
	"rm -rf", "rm -r /", "chmod 777", "chmod -R 777",
	"kill -9", "killall", "pkill",
	"dd if=", "mkfs", "fdisk",
	"iptables", "ufw", "firewall-cmd",
	"shutdown", "reboot", "halt",
	":(){ :|:& };:", // fork bomb
}

// PermissionRequest represents a permission check from a sub-agent to its parent.
type PermissionRequest struct {
	AgentID    string
	AgentName  string
	ToolName   string
	Input      string
	ResponseCh chan PermissionResponse
}

// PermissionResponse is the parent's decision on a permission request.
type PermissionResponse struct {
	Allowed bool
	Reason  string
}

// PermissionEvaluator evaluates tool permissions for sub-agents based on their PermissionMode.
type PermissionEvaluator struct {
	mode     PermissionMode
	parentCh chan<- PermissionRequest // for bubble mode: send requests to parent
}

// NewPermissionEvaluator creates a new evaluator for the given mode.
// parentCh is only required for bubble mode; pass nil for other modes.
func NewPermissionEvaluator(mode PermissionMode, parentCh chan<- PermissionRequest) *PermissionEvaluator {
	return &PermissionEvaluator{
		mode:     mode,
		parentCh: parentCh,
	}
}

// Check evaluates whether a tool execution should be allowed.
// Returns (allowed, reason).
func (pe *PermissionEvaluator) Check(ctx context.Context, toolName, input string) (bool, string) {
	switch pe.mode {
	case PermModeBypass:
		return true, "bypass: all operations allowed"

	case PermModeAcceptEdits:
		if IsDangerousOperation(toolName, input) {
			// Dangerous operations bubble to parent if channel available
			if pe.parentCh != nil {
				return pe.bubbleToParent(ctx, toolName, input)
			}
			return false, fmt.Sprintf("accept_edits: dangerous operation blocked: %s", toolName)
		}
		return true, "accept_edits: auto-approved"

	case PermModeBubble:
		if pe.parentCh == nil {
			return false, "bubble: no parent channel available"
		}
		return pe.bubbleToParent(ctx, toolName, input)

	default:
		// PermModeDefault: defer to existing permission checks (return allowed)
		return true, "default: deferred to standard permission checks"
	}
}

// bubbleToParent sends a permission request to the parent and waits for response.
func (pe *PermissionEvaluator) bubbleToParent(ctx context.Context, toolName, input string) (bool, string) {
	req := PermissionRequest{
		ToolName:   toolName,
		Input:      input,
		ResponseCh: make(chan PermissionResponse, 1),
	}

	// Send request to parent
	select {
	case pe.parentCh <- req:
		// OK, request sent
	case <-ctx.Done():
		return false, "bubble: context cancelled while sending request"
	case <-time.After(5 * time.Second):
		return false, "bubble: timeout sending request to parent"
	}

	// Wait for response
	select {
	case resp := <-req.ResponseCh:
		return resp.Allowed, resp.Reason
	case <-ctx.Done():
		return false, "bubble: context cancelled while waiting for response"
	case <-time.After(30 * time.Second):
		return false, "bubble: timeout waiting for parent response"
	}
}

// IsDangerousOperation checks if a tool+input combination is dangerous.
// Used by accept_edits mode to decide whether to auto-approve or escalate.
func IsDangerousOperation(toolName, input string) bool {
	// Only bash/shell commands can be dangerous in this context
	lowerName := strings.ToLower(toolName)
	if lowerName != "bash" && lowerName != "shell" && lowerName != "execute" {
		return false
	}

	lowerInput := strings.ToLower(input)
	for _, cmd := range dangerousCommands {
		if strings.Contains(lowerInput, cmd) {
			return true
		}
	}
	return false
}

// Mode returns the evaluator's permission mode.
func (pe *PermissionEvaluator) Mode() PermissionMode {
	return pe.mode
}
