package eval

import (
	"context"
	"sync"

	"github.com/Forest-Isle/IronClaw/internal/evolution"
)

// EvalHook implements evolution.Hook to capture metrics during evaluation runs.
// It accumulates reflection, episode, tool-execution, and context-compression
// data per session, which the CognitiveAgentRunner reads after each task completes.
type EvalHook struct {
	mu           sync.Mutex
	reflections  map[string]*evolution.ReflectionEvent
	episodes     map[string]*evolution.EpisodeEvent
	toolExecs    map[string][]evolution.ToolExecEvent
	compressions map[string][]CompressionEvent
}

var _ evolution.Hook = (*EvalHook)(nil)

// NewEvalHook creates a hook for capturing eval metrics.
func NewEvalHook() *EvalHook {
	return &EvalHook{
		reflections:  make(map[string]*evolution.ReflectionEvent),
		episodes:     make(map[string]*evolution.EpisodeEvent),
		toolExecs:    make(map[string][]evolution.ToolExecEvent),
		compressions: make(map[string][]CompressionEvent),
	}
}

func (h *EvalHook) Name() string { return "eval_hook" }

func (h *EvalHook) OnReflectionComplete(_ context.Context, event evolution.ReflectionEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reflections[event.SessionID] = &event
}

func (h *EvalHook) OnEpisodeComplete(_ context.Context, event evolution.EpisodeEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.episodes[event.SessionID] = &event
}

func (h *EvalHook) OnToolExecuted(_ context.Context, event evolution.ToolExecEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.toolExecs[event.SessionID] = append(h.toolExecs[event.SessionID], event)
}

// GetReflection returns the last captured reflection for a session.
func (h *EvalHook) GetReflection(sessionID string) *evolution.ReflectionEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reflections[sessionID]
}

// GetEpisode returns the last captured episode for a session.
func (h *EvalHook) GetEpisode(sessionID string) *evolution.EpisodeEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.episodes[sessionID]
}

// GetToolExecs returns all captured tool executions for a session.
func (h *EvalHook) GetToolExecs(sessionID string) []evolution.ToolExecEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	execs := h.toolExecs[sessionID]
	out := make([]evolution.ToolExecEvent, len(execs))
	copy(out, execs)
	return out
}

// RecordCompression records one context compression event for the session.
// Called by the compressionAdapter wired into the context manager.
func (h *EvalHook) RecordCompression(sessionID, reason string, layersRun int, beforePct, afterPct float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.compressions[sessionID] = append(h.compressions[sessionID], CompressionEvent{
		Reason:    reason,
		LayersRun: layersRun,
		BeforePct: beforePct,
		AfterPct:  afterPct,
	})
}

// GetCompressions returns all compression events captured for a session.
func (h *EvalHook) GetCompressions(sessionID string) []CompressionEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	events := h.compressions[sessionID]
	out := make([]CompressionEvent, len(events))
	copy(out, events)
	return out
}

// ClearSession removes captured data for a session (call between tasks).
func (h *EvalHook) ClearSession(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.reflections, sessionID)
	delete(h.episodes, sessionID)
	delete(h.toolExecs, sessionID)
	delete(h.compressions, sessionID)
}
