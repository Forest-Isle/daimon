package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

const maxOutputSize = 64 * 1024 // 64KB
const largeOutputThreshold = 8 * 1024 // 8KB

type bashOutput struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	Truncated  bool   `json:"truncated"`
	DurationMs int64  `json:"duration_ms"`
	Status     string `json:"status"`
	FilePath   string `json:"file_path,omitempty"`
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

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

	if msg := b.policy.CheckBashCommand(in.Command); msg != "" {
		return Result{Error: msg}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", in.Command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Run()
	durationMs := time.Since(start).Milliseconds()

	if runErr != nil && ctx.Err() == context.DeadlineExceeded {
		return Result{Error: fmt.Sprintf("command timed out after %s", b.timeout)}, nil
	}

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	status := "ok"
	if exitCode != 0 {
		status = "failed"
	}

	stdoutStr := truncateStr(stdout.String(), maxOutputSize)
	stderrStr := truncateStr(stderr.String(), maxOutputSize)

	out := bashOutput{
		Stdout:     stdoutStr,
		Stderr:     stderrStr,
		ExitCode:   exitCode,
		DurationMs: durationMs,
		Status:     status,
	}

	totalSize := len(stdoutStr) + len(stderrStr)
	truncated := totalSize > largeOutputThreshold

	result := Result{
		Metadata: map[string]any{
			"exit_code":   exitCode,
			"status":      status,
			"duration_ms": durationMs,
		},
	}

	if truncated {
		fullJSON, _ := json.Marshal(out)
		tmpFile, err := os.CreateTemp("", "ironclaw-bash-*.json")
		if err != nil {
			return Result{Error: fmt.Sprintf("failed to create temp file: %v", err)}, nil
		}
		tmpFile.Write(fullJSON)
		tmpFile.Close()

		out.Stdout = truncateStr(stdoutStr, largeOutputThreshold/2)
		out.Stderr = truncateStr(stderrStr, largeOutputThreshold/2)
		out.Truncated = true
		out.FilePath = tmpFile.Name()
		result.IsPartial = true
	}

	outputJSON, _ := json.Marshal(out)
	result.Output = string(outputJSON)

	if exitCode != 0 {
		result.Error = fmt.Sprintf("exit code %d", exitCode)
	}

	return result, nil
}
