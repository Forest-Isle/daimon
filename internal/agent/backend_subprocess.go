package agent

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"
)

// SubprocessBackend executes agents as child processes via os/exec.
// The child process runs `ironclaw agent run`, receiving a SubprocessRequest
// via stdin and returning a SubprocessResponse via stdout (JSON over stdio).
//
// The child process bootstraps its own LLM provider, DB, and tool registry
// from the config file at configPath. MCP tools are NOT available in
// subprocess mode — only built-in tools (bash, file, http, browser).
type SubprocessBackend struct {
	configPath string // config file path passed to the child process
}

// NewSubprocessBackend creates a subprocess backend.
// configPath is the ironclaw config file the child process will load.
func NewSubprocessBackend(configPath string) *SubprocessBackend {
	return &SubprocessBackend{configPath: configPath}
}

// Execute launches a child process to run the agent task.
// Returns a channel that receives exactly one AgentResult, then closes.
func (b *SubprocessBackend) Execute(ctx context.Context, cfg BackendConfig) (<-chan *AgentResult, error) {
	binary, err := findIronclawBinary()
	if err != nil {
		return nil, fmt.Errorf("subprocess: %w", err)
	}

	ch := make(chan *AgentResult, 1)
	go func() {
		defer close(ch)
		start := time.Now()

		result, err := b.runProcess(ctx, binary, cfg)
		if err != nil {
			ch <- &AgentResult{
				AgentName: cfg.Spec.Name,
				Error:     err,
				Duration:  time.Since(start),
			}
			return
		}
		result.Duration = time.Since(start)
		ch <- result
	}()

	return ch, nil
}

// Available reports whether the ironclaw binary can be found.
func (b *SubprocessBackend) Available() bool {
	_, err := findIronclawBinary()
	return err == nil
}

func (b *SubprocessBackend) Name() string   { return string(BackendSubprocess) }
func (b *SubprocessBackend) Cleanup() error { return nil }

// runProcess spawns `ironclaw agent run`, writes SubprocessRequest to stdin,
// and reads SubprocessResponse from stdout.
func (b *SubprocessBackend) runProcess(ctx context.Context, binary string, cfg BackendConfig) (*AgentResult, error) {
	req := &SubprocessRequest{
		AgentID:      fmt.Sprintf("sub_%s_%d", cfg.Spec.Name, time.Now().UnixNano()),
		Task:         cfg.Task,
		TaskContext:  cfg.TaskContext,
		SystemPrompt: cfg.Spec.SystemPrompt,
		Model:        cfg.Spec.Model,
		MaxTokens:    cfg.Spec.MaxTokens,
		MaxIter:      cfg.Spec.MaxIterations,
		AllowedTools: cfg.Spec.Tools,
		ConfigPath:   b.configPath,
		EnvVars:      cfg.EnvVars,
		Timeout:      cfg.Spec.Timeout.Duration().String(),
	}

	// Serialize request to buffer for stdin.
	var stdinBuf bytes.Buffer
	if err := WriteRequest(&stdinBuf, req); err != nil {
		return nil, fmt.Errorf("subprocess: %w", err)
	}

	cmd := exec.CommandContext(ctx, binary, "agent", "run")
	cmd.Stdin = &stdinBuf

	// Capture stdout (JSON response) and stderr (logs).
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Propagate env vars from config.
	if len(cfg.EnvVars) > 0 {
		cmd.Env = os.Environ()
		for k, v := range cfg.EnvVars {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	slog.Info("subprocess: starting child process",
		"binary", binary,
		"agent", cfg.Spec.Name,
		"config", b.configPath,
	)

	if err := cmd.Run(); err != nil {
		// If stdout has content, the child may have written an error response.
		if stdout.Len() > 0 {
			resp, parseErr := ReadResponse(&stdout)
			if parseErr == nil && resp.Error != "" {
				return resp.ToAgentResult(cfg.Spec.Name), nil
			}
		}
		return nil, fmt.Errorf("subprocess exited: %w (stderr: %s)", err, stderr.String())
	}

	// Parse JSON response from stdout.
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("subprocess: empty stdout (stderr: %s)", stderr.String())
	}

	resp, err := ReadResponse(&stdout)
	if err != nil {
		return nil, fmt.Errorf("subprocess: parse response: %w (raw: %s)", err, stdout.String())
	}

	return resp.ToAgentResult(cfg.Spec.Name), nil
}

// findIronclawBinary resolves the ironclaw binary path.
// Search order:
//  1. IRONCLAW_BINARY environment variable
//  2. Current executable (os.Executable)
//  3. PATH lookup
func findIronclawBinary() (string, error) {
	// 1. Explicit env var
	if envBin := os.Getenv("IRONCLAW_BINARY"); envBin != "" {
		if _, err := os.Stat(envBin); err == nil {
			return envBin, nil
		}
	}

	// 2. Current executable (self-invoke)
	if exe, err := os.Executable(); err == nil {
		if _, err := os.Stat(exe); err == nil {
			return exe, nil
		}
	}

	// 3. Search PATH
	if p, err := exec.LookPath("ironclaw"); err == nil {
		return p, nil
	}

	return "", fmt.Errorf("ironclaw binary not found (set IRONCLAW_BINARY or ensure ironclaw is in PATH)")
}
