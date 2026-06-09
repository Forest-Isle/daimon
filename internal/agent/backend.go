package agent

import (
	"context"
	"fmt"
	"time"
)


// BackendType identifies which execution backend to use.
type BackendType string

const (
	BackendInProcess  BackendType = "in_process" // goroutine (default)
	BackendSubprocess BackendType = "subprocess" // os/exec child process
	BackendDocker     BackendType = "docker"     // container execution
)

// BackendConfig holds everything an execution backend needs to run an agent.
// ParentRuntime is intentionally excluded — it is not serializable and backends
// that run out-of-process (subprocess, docker) cannot receive it.
type BackendConfig struct {
	Spec        *AgentSpec
	Task        string
	TaskContext string            // optional context from upstream tasks
	EnvVars     map[string]string // extra environment variables for out-of-process backends
}

// ExecutionBackend abstracts the environment in which a sub-agent runs.
type ExecutionBackend interface {
	// Execute runs the agent task and returns a result channel.
	// The channel receives exactly one result, then is closed.
	Execute(ctx context.Context, config BackendConfig) (<-chan *AgentResult, error)

	// Available returns true if this backend can be used.
	Available() bool

	// Name returns the backend identifier.
	Name() string

	// Cleanup releases resources held by this backend.
	Cleanup() error
}

// --- InProcessBackend ---

// InProcessBackend executes agents as goroutines within the current process.
// This is the default backend with zero spawn overhead.
type InProcessBackend struct {
	// executor is the function that actually runs the agent.
	// Injected for testability; in production this calls AgentTool.executeSpawn.
	executor func(ctx context.Context, cfg BackendConfig) (*AgentResult, error)
}

// NewInProcessBackend creates a new in-process backend.
// If executor is nil, a default no-op executor is used (wire the real one at integration time).
func NewInProcessBackend(executor func(ctx context.Context, cfg BackendConfig) (*AgentResult, error)) *InProcessBackend {
	return &InProcessBackend{executor: executor}
}

func (b *InProcessBackend) Execute(ctx context.Context, cfg BackendConfig) (<-chan *AgentResult, error) {
	if b.executor == nil {
		return nil, fmt.Errorf("in-process backend: no executor configured")
	}

	ch := make(chan *AgentResult, 1)
	go func() {
		defer close(ch)
		start := time.Now()
		result, err := b.executor(ctx, cfg)
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

func (b *InProcessBackend) Available() bool { return true }
func (b *InProcessBackend) Name() string    { return string(BackendInProcess) }
func (b *InProcessBackend) Cleanup() error  { return nil }

// SelectBackend returns the appropriate backend for the given type.
// If the requested backend is unavailable, it transparently falls back to
// InProcessBackend to ensure the system always works.
func SelectBackend(backendType BackendType, executor func(ctx context.Context, cfg BackendConfig) (*AgentResult, error), opts ...BackendOption) ExecutionBackend {
	o := backendOptions{
		dockerImage: "ironclaw:latest",
	}
	for _, opt := range opts {
		opt(&o)
	}

	switch backendType {
	case BackendSubprocess:
		be := NewSubprocessBackend(o.configPath)
		if be.Available() {
			return be
		}
		return NewInProcessBackend(executor)
	case BackendDocker:
		be := NewDockerBackend(o.dockerImage, o.dockerNetwork)
		if be.Available() {
			return be
		}
		return NewInProcessBackend(executor)
	default:
		return NewInProcessBackend(executor)
	}
}

// --- BackendOption ---

// BackendOption configures backend selection via SelectBackend.
type BackendOption func(*backendOptions)

type backendOptions struct {
	configPath    string // config file path for SubprocessBackend
	dockerImage   string // container image for DockerBackend
	dockerNetwork string // Docker network for DockerBackend
}

// WithConfigPath provides the config file path for SubprocessBackend.
func WithConfigPath(p string) BackendOption {
	return func(o *backendOptions) { o.configPath = p }
}

// WithDockerImage provides the Docker image and network for DockerBackend.
func WithDockerImage(img, network string) BackendOption {
	return func(o *backendOptions) { o.dockerImage = img; o.dockerNetwork = network }
}
