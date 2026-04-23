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
	SessionID      string
	EpisodeID      string
	Goal           string
	Complexity     string
	Succeeded      bool
	TotalReward    float64
	ToolSequence   []string   // ordered tool names used
	LessonsLearned []string   // from REFLECT (cognitive), empty for simple mode
	ReplanCount    int
	DurationMs     int64
	UserFeedback   float64
	Timestamp      time.Time
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
	cfg     Config
	hooks   []Hook
	router  *ModelRouter
	trajDir string // trajectory directory for insights loop (empty = disabled)
	mu      sync.RWMutex
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewEngine creates a new evolution engine. Call Start() to begin processing.
func NewEngine(cfg Config) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{
		cfg:    cfg,
		router: NewModelRouter(cfg.Router),
		ctx:    ctx,
		cancel: cancel,
	}
}

// Router returns the model router (never nil, even when routing is disabled).
func (e *Engine) Router() *ModelRouter {
	return e.router
}

// SetTrajectoryDir sets the trajectory directory for the insights feedback
// loop. Must be called before Start().
func (e *Engine) SetTrajectoryDir(dir string) {
	e.trajDir = dir
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
	e.mu.RLock()
	hookCount := len(e.hooks)
	e.mu.RUnlock()
	if hookCount == 0 {
		slog.Warn("evolution: engine enabled but no hooks registered")
	}

	// Launch insights → optimizer feedback loop if we have both a trajectory
	// dir and a strategy optimizer.
	if e.trajDir != "" && e.StrategyOptimizerHook() != nil {
		e.wg.Add(1)
		go e.insightsLoop()
	}

	slog.Info("evolution: engine started", "hooks", hookCount)
}

// insightsLoop periodically generates insights from trajectory data and feeds
// recommendations into the StrategyOptimizer. Runs every 6 hours.
func (e *Engine) insightsLoop() {
	defer e.wg.Done()

	const interval = 6 * time.Hour
	timer := time.NewTimer(interval)
	defer timer.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-timer.C:
			e.RunInsightsCycle()
			timer.Reset(interval)
		}
	}
}

// RunInsightsCycle generates insights from recent trajectory data and applies
// recommendations to the strategy optimizer and preference learner. Safe to
// call from external code (e.g. eval longitudinal) to force an immediate
// learning cycle without waiting for the 6-hour timer.
func (e *Engine) RunInsightsCycle() {
	so := e.StrategyOptimizerHook()
	if so == nil {
		return
	}

	since := time.Now().Add(-7 * 24 * time.Hour)
	records, err := ReadTrajectories(e.trajDir, since, time.Now())
	if err != nil {
		slog.Warn("evolution: insights cycle read failed", "err", err)
		return
	}
	if len(records) < 5 {
		return // not enough data
	}

	report := GenerateInsights(records, "auto-7d")
	applied := so.ApplyInsights(report)
	if applied > 0 {
		slog.Info("evolution: insights cycle complete",
			"adjustments", applied,
			"episodes_analyzed", report.TotalEpisodes,
		)
	}

	if pl := e.PreferenceLearnerHook(); pl != nil {
		plApplied := pl.ApplyInsights(report)
		if plApplied > 0 {
			slog.Info("evolution: insights → preferences updated", "adjustments", plApplied)
		}
	}
}

// SaveState persists in-memory state for hooks that support it (e.g.
// PreferenceLearner). Call before Stop() for a clean shutdown.
func (e *Engine) SaveState(prefPath string) {
	if pl := e.PreferenceLearnerHook(); pl != nil && prefPath != "" {
		if err := pl.SavePreferences(prefPath); err != nil {
			slog.Warn("evolution: failed to save preferences", "err", err)
		} else {
			slog.Info("evolution: preferences saved", "path", prefPath)
		}
	}
	if so := e.StrategyOptimizerHook(); so != nil && e.cfg.Optimizer.StrategyFile != "" {
		if err := so.SaveStrategy(e.cfg.Optimizer.StrategyFile); err != nil {
			slog.Warn("evolution: failed to save strategy on shutdown", "err", err)
		}
	}
}

// Stop gracefully shuts down the engine, waiting for in-flight hooks to finish.
// Hooks that implement io.Closer (e.g. TrajectoryRecorder) are closed.
func (e *Engine) Stop() {
	e.cancel()
	e.wg.Wait()

	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, h := range e.hooks {
		if c, ok := h.(interface{ Close() error }); ok {
			if err := c.Close(); err != nil {
				slog.Warn("evolution: hook close failed", "hook", h.Name(), "err", err)
			}
		}
	}
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

// DispatchToolExec fires OnToolExecuted on all hooks synchronously.
// Unlike DispatchReflection/DispatchEpisode, this blocks until all hooks
// finish so that tool events are buffered before episode dispatch starts.
func (e *Engine) DispatchToolExec(event ToolExecEvent) {
	if !e.cfg.Enabled {
		return
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, h := range e.hooks {
		hook := h
		e.wg.Add(1)
		e.safeDispatch(hook.Name(), func(ctx context.Context) {
			hook.OnToolExecuted(ctx, event)
		})
	}
}

// WaitPending blocks until all in-flight hook dispatches complete.
// Callers (e.g. eval runner) can use this to ensure hook side-effects
// are visible before reading hook state.
func (e *Engine) WaitPending() {
	e.wg.Wait()
}

// PreferenceLearnerHook returns the first registered PreferenceLearner hook, or nil.
func (e *Engine) PreferenceLearnerHook() *PreferenceLearner {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, h := range e.hooks {
		if pl, ok := h.(*PreferenceLearner); ok {
			return pl
		}
	}
	return nil
}

// SkillSynthesizerHook returns the first registered SkillSynthesizer hook, or nil.
func (e *Engine) SkillSynthesizerHook() *SkillSynthesizer {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, h := range e.hooks {
		if ss, ok := h.(*SkillSynthesizer); ok {
			return ss
		}
	}
	return nil
}

// TrajectoryRecorderHook returns the first registered TrajectoryRecorder hook, or nil.
func (e *Engine) TrajectoryRecorderHook() *TrajectoryRecorder {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, h := range e.hooks {
		if tr, ok := h.(*TrajectoryRecorder); ok {
			return tr
		}
	}
	return nil
}

// TrajectoryDir returns the configured trajectory directory (may be empty).
func (e *Engine) TrajectoryDir() string {
	return e.trajDir
}

// StrategyOptimizerHook returns the first registered StrategyOptimizer hook, or nil.
func (e *Engine) StrategyOptimizerHook() *StrategyOptimizer {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, h := range e.hooks {
		if so, ok := h.(*StrategyOptimizer); ok {
			return so
		}
	}
	return nil
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
