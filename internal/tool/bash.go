package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/Forest-Isle/IronClaw/internal/util"
	"os"
	"os/exec"
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
}

type bashInput struct {
	Command string `json:"command"`
}

func NewBashTool(timeout time.Duration, requiresApproval bool, policy *Policy) *BashTool {
	return &BashTool{timeout: timeout, approval: requiresApproval, policy: policy}
}

func (b *BashTool) Name() string           { return "bash" }
func (b *BashTool) Description() string    { return "Execute a shell command and return its output." }
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
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout

	// If a StreamCallback is attached to the context, tee stdout through it
	// for real-time output in channels that support ToolStreamWriter.
	streamCB := StreamCallbackFromContext(ctx)

	start := time.Now()
	var runErr error

	if streamCB != nil {
		// Use a pipe so we can read stdout while the command runs.
		stdoutPipe, pipeErr := cmd.StdoutPipe()
		if pipeErr != nil {
			return Result{Error: fmt.Sprintf("stdout pipe: %v", pipeErr)}, nil
		}
		if startErr := cmd.Start(); startErr != nil {
			return Result{Error: fmt.Sprintf("command start: %v", startErr)}, nil
		}
		// Read chunks from stdout, tee to buffer and stream callback.
		buf := make([]byte, 4096)
		for {
			n, readErr := stdoutPipe.Read(buf)
			if n > 0 {
				stdout.Write(buf[:n])
				streamCB(string(buf[:n]))
			}
			if readErr != nil {
				break
			}
		}
		runErr = cmd.Wait()
	} else {
		runErr = cmd.Run()
	}

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

	stdoutStr := util.TruncateStr(stdout.String(), maxOutputSize)
	stderrStr := util.TruncateStr(stderr.String(), maxOutputSize)

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

	outputJSON, _ := json.Marshal(out)
	result.Output = string(outputJSON)

	if exitCode != 0 {
		result.Error = fmt.Sprintf("exit code %d", exitCode)
	}

	return result, nil
}
