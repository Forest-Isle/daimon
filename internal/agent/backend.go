package agent

import (
	"context"
	"fmt"
	"time"
)


// BackendType identifies which execution backend to use.
type BackendType string

const (
	BackendInProcess BackendType = "in_process" // goroutine (default)
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

