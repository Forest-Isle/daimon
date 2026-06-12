package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Forest-Isle/daimon/internal/util"
	"os"
	"time"
)

const maxOutputSize = 64 * 1024       // 64KB
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

type BashTool struct {
	timeout  time.Duration
	approval bool
	policy   *Policy
	backend  ShellBackend
}

type bashInput struct {
	Command string `json:"command"`
}

func NewBashTool(timeout time.Duration, requiresApproval bool, policy *Policy) *BashTool {
	return NewBashToolWithBackend(timeout, requiresApproval, policy, NewHostShellBackend())
}

func NewBashToolWithBackend(timeout time.Duration, requiresApproval bool, policy *Policy, backend ShellBackend) *BashTool {
	if backend == nil {
		backend = NewHostShellBackend()
	}
	if policy == nil {
		policy = NewPolicy(nil)
	}
	return &BashTool{timeout: timeout, approval: requiresApproval, policy: policy, backend: backend}
}

func (b *BashTool) Name() string           { return "bash" }
func (b *BashTool) Description() string    { return "Execute a shell command and return its output." }
func (b *BashTool) RequiresApproval() bool { return b.approval }

// Available checks whether the bash shell executable can be found on the host.
func (b *BashTool) Available() bool {
	return b.backend != nil && b.backend.Available()
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

	start := time.Now()
	run, runErr := b.backend.Run(ctx, in.Command, WorkDirFromContext(ctx), StreamCallbackFromContext(ctx))
	durationMs := time.Since(start).Milliseconds()

	if ctx.Err() == context.DeadlineExceeded {
		return Result{Error: fmt.Sprintf("command timed out after %s", b.timeout)}, nil
	}
	if runErr != nil {
		return Result{Error: fmt.Sprintf("command execution: %v", runErr)}, nil
	}

	status := "ok"
	if run.ExitCode != 0 {
		status = "failed"
	}

	stdoutStr := util.TruncateStr(run.Stdout, maxOutputSize)
	stderrStr := util.TruncateStr(run.Stderr, maxOutputSize)

	out := bashOutput{
		Stdout:     stdoutStr,
		Stderr:     stderrStr,
		ExitCode:   run.ExitCode,
		DurationMs: durationMs,
		Status:     status,
	}

	totalSize := len(stdoutStr) + len(stderrStr)
	truncated := totalSize > largeOutputThreshold

	result := Result{
		Metadata: map[string]any{
			"exit_code":   run.ExitCode,
			"status":      status,
			"duration_ms": durationMs,
		},
	}

	if truncated {
		fullJSON, marshalErr := json.Marshal(out)
		if marshalErr != nil {
			return Result{Error: fmt.Sprintf("failed to marshal bash output for temp file: %v", marshalErr)}, nil
		}
		tmpFile, err := os.CreateTemp("", "daimon-bash-*.json")
		if err != nil {
			return Result{Error: fmt.Sprintf("failed to create temp file: %v", err)}, nil
		}
		if _, wErr := tmpFile.Write(fullJSON); wErr != nil {
			_ = tmpFile.Close()
			return Result{Error: fmt.Sprintf("failed to write temp file: %v", wErr)}, nil
		}
		if cErr := tmpFile.Close(); cErr != nil {
			return Result{Error: fmt.Sprintf("failed to close temp file: %v", cErr)}, nil
		}

		out.Stdout = util.TruncateStr(stdoutStr, largeOutputThreshold/2)
		out.Stderr = util.TruncateStr(stderrStr, largeOutputThreshold/2)
		out.Truncated = true
		out.FilePath = tmpFile.Name()
		result.IsPartial = true
	}

	outputJSON, err := json.Marshal(out)
	if err != nil {
		return Result{Error: fmt.Sprintf("failed to marshal bash output: %v", err)}, nil
	}
	result.Output = string(outputJSON)

	if run.ExitCode != 0 {
		result.Error = fmt.Sprintf("exit code %d", run.ExitCode)
	}

	return result, nil
}
