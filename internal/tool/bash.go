package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

const maxOutputSize = 64 * 1024 // 64KB

type BashTool struct {
	timeout  time.Duration
	approval bool
	policy   *Policy
}

type bashInput struct {
	Command string `json:"command"`
}

func NewBashTool(timeout time.Duration, requiresApproval bool, policy *Policy) *BashTool {
	return &BashTool{timeout: timeout, approval: requiresApproval, policy: policy}
}

func (b *BashTool) Name() string        { return "bash" }
func (b *BashTool) Description() string  { return "Execute a shell command and return its output." }
func (b *BashTool) RequiresApproval() bool { return b.approval }

// Available checks whether the bash shell executable can be found on the host.
func (b *BashTool) Available() bool {
	_, err := exec.LookPath("bash")
	return err == nil
}

// IsReadOnly returns false because bash commands may have arbitrary side effects.
func (b *BashTool) IsReadOnly() bool { return false }

// Capabilities returns the bash tool's capabilities.
func (b *BashTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		IsReadOnly:      false, // bash can have arbitrary side effects
		IsDestructive:   true,  // bash can delete files, kill processes, etc.
		RequiresNetwork: false,
		ApprovalMode:    "always",
	}
}

func (b *BashTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to execute",
			},
		},
		"required": []string{"command"},
	}
}

func (b *BashTool) Execute(ctx context.Context, input []byte) (Result, error) {
	var in bashInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: "invalid input: " + err.Error()}, nil
	}

	if in.Command == "" {
		return Result{Error: "command is required"}, nil
	}

	// Check policy
	if msg := b.policy.CheckBashCommand(in.Command); msg != "" {
		return Result{Error: msg}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", in.Command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\nSTDERR:\n" + stderr.String()
	}

	// Truncate large output
	if len(output) > maxOutputSize {
		output = output[:maxOutputSize] + "\n... (output truncated)"
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return Result{Error: fmt.Sprintf("command timed out after %s", b.timeout)}, nil
		}
		return Result{Output: output, Error: err.Error()}, nil
	}

	return Result{Output: output}, nil
}
