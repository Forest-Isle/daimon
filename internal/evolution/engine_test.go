package evolution

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testHook implements Hook for testing.
type testHook struct {
	name           string
	reflections    atomic.Int32
	episodes       atomic.Int32
	tools          atomic.Int32
	lastReflection ReflectionEvent
	lastEpisode    EpisodeEvent
	lastTool       ToolExecEvent
	mu             sync.Mutex
}

func (h *testHook) Name() string { return h.name }

func (h *testHook) OnReflectionComplete(_ context.Context, event ReflectionEvent) {
	h.reflections.Add(1)
	h.mu.Lock()
	h.lastReflection = event
	h.mu.Unlock()
}

func (h *testHook) OnEpisodeComplete(_ context.Context, event EpisodeEvent) {
	h.episodes.Add(1)
	h.mu.Lock()
	h.lastEpisode = event
	h.mu.Unlock()
}

func (h *testHook) OnToolExecuted(_ context.Context, event ToolExecEvent) {
	h.tools.Add(1)
	h.mu.Lock()
	h.lastTool = event
	h.mu.Unlock()
}

func TestEngine_DispatchReflection(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true

	engine := NewEngine(cfg)
	hook := &testHook{name: "test"}
	engine.RegisterHook(hook)
	engine.Start()
	defer engine.Stop()

	event := ReflectionEvent{
		SessionID:  "sess-1",
		UserID:     "user-1",
		Goal:       "fix bug",
		Succeeded:  true,
		Confidence: 0.9,
		Timestamp:  time.Now(),
	}
	engine.DispatchReflection(event)

	// Wait for async dispatch
	time.Sleep(50 * time.Millisecond)

	if hook.reflections.Load() != 1 {
		t.Errorf("expected 1 reflection dispatch, got %d", hook.reflections.Load())
	}
	hook.mu.Lock()
	if hook.lastReflection.Goal != "fix bug" {
		t.Errorf("expected goal 'fix bug', got %q", hook.lastReflection.Goal)
	}
	hook.mu.Unlock()
}

func TestEngine_DispatchEpisode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true

	engine := NewEngine(cfg)
	hook := &testHook{name: "test"}
	engine.RegisterHook(hook)
	engine.Start()
	defer engine.Stop()

	event := EpisodeEvent{
		SessionID:    "sess-1",
		EpisodeID:    "ep-1",
		Succeeded:    true,
		TotalReward:  0.8,
		ToolSequence: []string{"bash", "file_write"},
		Timestamp:    time.Now(),
	}
	engine.DispatchEpisode(event)

	time.Sleep(50 * time.Millisecond)

	if hook.episodes.Load() != 1 {
		t.Errorf("expected 1 episode dispatch, got %d", hook.episodes.Load())
	}
}

func TestEngine_DispatchToolExec(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true

	engine := NewEngine(cfg)
	hook := &testHook{name: "test"}
	engine.RegisterHook(hook)
	engine.Start()
	defer engine.Stop()

	event := ToolExecEvent{
		ToolName:   "bash",
		Succeeded:  true,
		DurationMs: 150,
		Timestamp:  time.Now(),
	}
	engine.DispatchToolExec(event)

	time.Sleep(50 * time.Millisecond)

	if hook.tools.Load() != 1 {
		t.Errorf("expected 1 tool dispatch, got %d", hook.tools.Load())
	}
}

func TestEngine_DisabledNoDispatch(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = false

	engine := NewEngine(cfg)
	hook := &testHook{name: "test"}
	engine.RegisterHook(hook)
	engine.Start()
	defer engine.Stop()

	engine.DispatchReflection(ReflectionEvent{})
	engine.DispatchEpisode(EpisodeEvent{})
	engine.DispatchToolExec(ToolExecEvent{})

	time.Sleep(50 * time.Millisecond)

	if hook.reflections.Load() != 0 || hook.episodes.Load() != 0 || hook.tools.Load() != 0 {
		t.Error("hooks should not fire when engine is disabled")
	}
}

// panicHook panics in every method to test recovery.
type panicHook struct{}

func (h *panicHook) Name() string { return "panic" }
func (h *panicHook) OnReflectionComplete(context.Context, ReflectionEvent) {
	panic("test panic in reflection")
}
func (h *panicHook) OnEpisodeComplete(context.Context, EpisodeEvent) {
	panic("test panic in episode")
}
func (h *panicHook) OnToolExecuted(context.Context, ToolExecEvent) {
	panic("test panic in tool")
}

func TestEngine_HookPanicRecovery(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true

	engine := NewEngine(cfg)
	engine.RegisterHook(&panicHook{})

	good := &testHook{name: "good"}
	engine.RegisterHook(good)

	engine.Start()
	defer engine.Stop()

	// Should not crash despite panic hook
	engine.DispatchReflection(ReflectionEvent{Goal: "survive"})
	time.Sleep(100 * time.Millisecond)

	if good.reflections.Load() != 1 {
		t.Error("good hook should still fire despite panic in another hook")
	}
}

func TestEngine_MultipleHooks(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true

	engine := NewEngine(cfg)
	hooks := make([]*testHook, 5)
	for i := range hooks {
		hooks[i] = &testHook{name: "test"}
		engine.RegisterHook(hooks[i])
	}

	engine.Start()
	defer engine.Stop()

	engine.DispatchReflection(ReflectionEvent{Goal: "multi"})
	time.Sleep(100 * time.Millisecond)

	for i, h := range hooks {
		if h.reflections.Load() != 1 {
			t.Errorf("hook %d: expected 1 reflection, got %d", i, h.reflections.Load())
		}
	}
}
