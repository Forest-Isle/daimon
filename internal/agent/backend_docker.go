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

// DockerBackend executes agents in Docker containers for full isolation.
// It uses the same stdin/stdout JSON IPC protocol as SubprocessBackend,
// running `ironclaw agent run` inside the container.
//
// Isolation semantics:
//   - The container uses an independent SQLite database (not shared with host).
//   - Memory and session data are NOT shared between host and container.
//   - Environment variables (API keys) are explicitly forwarded.
//   - Only built-in tools are available (no MCP).
//
// Use SubprocessBackend if you need shared memory/session state.
type DockerBackend struct {
	image   string
	network string
}

// NewDockerBackend creates a Docker backend.
// image is the Docker image name (e.g. "ironclaw:latest").
// network is an optional Docker network name (empty for default).
func NewDockerBackend(image, network string) *DockerBackend {
	if image == "" {
		image = "ironclaw:latest"
	}
	return &DockerBackend{image: image, network: network}
}

// Execute runs the agent task inside a Docker container.
func (b *DockerBackend) Execute(ctx context.Context, cfg BackendConfig) (<-chan *AgentResult, error) {
	if !b.Available() {
		return nil, fmt.Errorf("docker: daemon not available")
	}

	ch := make(chan *AgentResult, 1)
	go func() {
		defer close(ch)
		start := time.Now()

		result, err := b.runContainer(ctx, cfg)
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

// Available checks if the Docker CLI is installed and the daemon is running.
func (b *DockerBackend) Available() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	// Quick check: can we talk to the daemon?
	cmd := exec.Command("docker", "info", "--format", "{{.ServerVersion}}")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

func (b *DockerBackend) Name() string   { return string(BackendDocker) }
func (b *DockerBackend) Cleanup() error { return nil }

// runContainer launches `docker run --rm -i <image> agent run` and communicates
// via the SubprocessRequest/SubprocessResponse JSON protocol.
func (b *DockerBackend) runContainer(ctx context.Context, cfg BackendConfig) (*AgentResult, error) {
	req := &SubprocessRequest{
		AgentID:      fmt.Sprintf("docker_%s_%d", cfg.Spec.Name, time.Now().UnixNano()),
		Task:         cfg.Task,
		TaskContext:  cfg.TaskContext,
		SystemPrompt: cfg.Spec.SystemPrompt,
		Model:        cfg.Spec.Model,
		MaxTokens:    cfg.Spec.MaxTokens,
		MaxIter:      cfg.Spec.MaxIterations,
		AllowedTools: cfg.Spec.Tools,
		ConfigPath:   "/app/configs/ironclaw.yaml", // container-internal path
		EnvVars:      cfg.EnvVars,
		Timeout:      cfg.Spec.Timeout.Duration().String(),
	}

	// Serialize request for stdin.
	var stdinBuf bytes.Buffer
	if err := WriteRequest(&stdinBuf, req); err != nil {
		return nil, fmt.Errorf("docker: %w", err)
	}

	// Build docker run arguments.
	args := []string{"run", "--rm", "-i"}

	// Forward API keys from host environment.
	for _, envKey := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
		if v := os.Getenv(envKey); v != "" {
			args = append(args, "-e", envKey+"="+v)
		}
	}

	// Forward extra env vars from BackendConfig.
	for k, v := range cfg.EnvVars {
		args = append(args, "-e", k+"="+v)
	}

	// Optional network.
	if b.network != "" {
		args = append(args, "--network", b.network)
	}

	// Image and command.
	args = append(args, b.image, "agent", "run")

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = &stdinBuf

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	slog.Info("docker: starting container",
		"image", b.image,
		"agent", cfg.Spec.Name,
		"network", b.network,
	)

	if err := cmd.Run(); err != nil {
		// Try to parse a response from stdout even on non-zero exit.
		if stdout.Len() > 0 {
			if resp, parseErr := ReadResponse(&stdout); parseErr == nil && resp.Error != "" {
				return resp.ToAgentResult(cfg.Spec.Name), nil
			}
		}
		return nil, fmt.Errorf("docker exited: %w (stderr: %s)", err, stderr.String())
	}

	if stdout.Len() == 0 {
		return nil, fmt.Errorf("docker: empty stdout (stderr: %s)", stderr.String())
	}

	resp, err := ReadResponse(&stdout)
	if err != nil {
		return nil, fmt.Errorf("docker: parse response: %w (raw: %s)", err, stdout.String())
	}

	return resp.ToAgentResult(cfg.Spec.Name), nil
}
