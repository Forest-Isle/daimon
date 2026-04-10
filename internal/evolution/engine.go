package evolution

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// ReflectionEvent carries data from a completed REFLECT phase to evolution hooks.
type ReflectionEvent struct {
	SessionID        string
	UserID           string
	Goal             string
	Complexity       string
	Succeeded        bool
	Confidence       float64
	LessonsLearned   []string
	ToolsUsed        []string // tool names used during this episode
	ReplanCount      int
	UserFeedback     float64 // -1 to 1 (from thumbs down/up), 0 if not collected
	FinalAnswer      string
	Timestamp        time.Time
}

// EpisodeEvent carries data from a completed RL episode recording.
type EpisodeEvent struct {
	SessionID    string
	EpisodeID    string
	Goal         string
	Complexity   string
	Succeeded    bool
	TotalReward  float64
	ToolSequence []string // ordered tool names used
	ReplanCount  int
	DurationMs   int64
	UserFeedback float64
	Timestamp    time.Time
}

// ToolExecEvent carries data from a single tool execution.
type ToolExecEvent struct {
	SessionID  string
	ToolName   string
	Succeeded  bool
	Denied     bool
	DurationMs int64
	Timestamp  time.Time
}

// Hook defines the interface for evolution event handlers.
// Implementations must be safe for concurrent use. Each method is called
// asynchronously in a goroutine with a timeout-bounded context.
type Hook interface {
	// Name returns a human-readable identifier for logging.
	Name() string

	// OnReflectionComplete is called after the cognitive agent's REFLECT phase.
	OnReflectionComplete(ctx context.Context, event ReflectionEvent)

	// OnEpisodeComplete is called after an RL episode is recorded.
	OnEpisodeComplete(ctx context.Context, event EpisodeEvent)

	// OnToolExecuted is called after each tool execution completes.
	OnToolExecuted(ctx context.Context, event ToolExecEvent)
}

// Engine manages the self-evolution lifecycle and dispatches events to hooks.
// It is safe for concurrent use.
type Engine struct {
	cfg    Config
	hooks  []Hook
	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewEngine creates a new evolution engine. Call Start() to begin processing.
func NewEngine(cfg Config) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{
		cfg:    cfg,
		ctx:    ctx,
		cancel: cancel,
	}
}

// RegisterHook adds a hook to the dispatch chain. Must be called before Start().
func (e *Engine) RegisterHook(h Hook) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.hooks = append(e.hooks, h)
	slog.Info("evolution: hook registered", "hook", h.Name())
}

// Start launches the evolution engine. No-op if disabled in config.
func (e *Engine) Start() {
	if !e.cfg.Enabled {
		slog.Info("evolution: engine disabled")
		return
	}
	slog.Info("evolution: engine started", "hooks", len(e.hooks))
}

// Stop gracefully shuts down the engine, waiting for in-flight hooks to finish.
func (e *Engine) Stop() {
	e.cancel()
	e.wg.Wait()
	slog.Info("evolution: engine stopped")
}

// IsEnabled returns whether the evolution engine is active.
func (e *Engine) IsEnabled() bool {
	return e.cfg.Enabled
}

// DispatchReflection fires OnReflectionComplete on all hooks asynchronously.
func (e *Engine) DispatchReflection(event ReflectionEvent) {
	if !e.cfg.Enabled {
		return
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, h := range e.hooks {
		hook := h // capture for goroutine
		e.wg.Add(1)
		go e.safeDispatch(hook.Name(), func(ctx context.Context) {
			hook.OnReflectionComplete(ctx, event)
		})
	}
}

// DispatchEpisode fires OnEpisodeComplete on all hooks asynchronously.
func (e *Engine) DispatchEpisode(event EpisodeEvent) {
	if !e.cfg.Enabled {
		return
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, h := range e.hooks {
		hook := h
		e.wg.Add(1)
		go e.safeDispatch(hook.Name(), func(ctx context.Context) {
			hook.OnEpisodeComplete(ctx, event)
		})
	}
}

// DispatchToolExec fires OnToolExecuted on all hooks asynchronously.
func (e *Engine) DispatchToolExec(event ToolExecEvent) {
	if !e.cfg.Enabled {
		return
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, h := range e.hooks {
		hook := h
		e.wg.Add(1)
		go e.safeDispatch(hook.Name(), func(ctx context.Context) {
			hook.OnToolExecuted(ctx, event)
		})
	}
}

// safeDispatch runs fn with a timeout and panic recovery. Always decrements wg.
func (e *Engine) safeDispatch(hookName string, fn func(ctx context.Context)) {
	defer e.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			slog.Error("evolution: hook panicked",
				"hook", hookName,
				"panic", r,
			)
		}
	}()

	timeout := e.cfg.HookTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(e.ctx, timeout)
	defer cancel()

	fn(ctx)
}
