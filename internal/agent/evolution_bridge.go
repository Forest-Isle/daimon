package agent

import (
	"log/slog"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/evolution"
	"github.com/Forest-Isle/IronClaw/internal/session"
)

// EvolutionBridge connects the agent's EventBus to the evolution Engine.
// It subscribes to ToolExecuted events and forwards them to DispatchToolExec.
// It also publishes loop-completion and episode-completion events to
// DispatchReflection and DispatchEpisode so that PreferenceLearner,
// StrategyOptimizer, SkillSynthesizer, TrajectoryRecorder, eval harness,
// and cogmetrics all receive the data they need.
type EvolutionBridge struct {
	engine *evolution.Engine
}

// NewEvolutionBridge creates a bridge. When engine is nil, all methods
// are safe no-ops.
func NewEvolutionBridge(engine *evolution.Engine) *EvolutionBridge {
	return &EvolutionBridge{engine: engine}
}

// Engine returns the underlying evolution engine, or nil.
func (b *EvolutionBridge) Engine() *evolution.Engine { return b.engine }

// Subscribe subscribes to the agent's event bus and forwards every
// ToolExecuted event to the evolution engine's DispatchToolExec.
func (b *EvolutionBridge) Subscribe(bus EventBus) {
	if b.engine == nil || bus == nil || !b.engine.IsEnabled() {
		return
	}
	bus.Subscribe(func(event Event) {
		te, ok := event.(ToolExecuted)
		if !ok {
			return
		}
		b.engine.DispatchToolExec(evolution.ToolExecEvent{
			SessionID:  te.SessionID,
			ToolName:   te.ToolName,
			Succeeded:  te.Succeeded,
			Denied:     strings.Contains(strings.ToLower(te.Error), "denied"),
			DurationMs: te.DurationMs,
			Timestamp:  time.Now(),
		})
	})
	slog.Info("evolution bridge: subscribed to agent event bus")
}

// OnLoopComplete dispatches a ReflectionEvent to the evolution engine.
// Called by the agent loop after each tool-dispatch batch within an iteration.
func (b *EvolutionBridge) OnLoopComplete(sessionID string, toolCalls int, successCount int, iteration int, toolNames ...string) {
	if b.engine == nil {
		return
	}
	b.engine.DispatchReflection(evolution.ReflectionEvent{
		SessionID:  sessionID,
		ToolsUsed:  toolNames,
		Timestamp:  time.Now(),
	})
	_ = toolCalls     // preserved for future use
	_ = successCount  // preserved for future use
	_ = iteration     // preserved for future use
}

// OnEpisodeComplete dispatches an EpisodeEvent to the evolution engine.
// Called when the agent loop finishes (success or failure).
func (b *EvolutionBridge) OnEpisodeComplete(sessionID string, iterations int, success bool, durationMs int64) {
	if b.engine == nil {
		return
	}
	b.engine.DispatchEpisode(evolution.EpisodeEvent{
		SessionID:  sessionID,
		Succeeded:  success,
		DurationMs: durationMs,
		Timestamp:  time.Now(),
	})
	_ = iterations // preserved for future use
}

// notifyLoopIteration is a shared helper called by both SimpleLoop and
// UnifiedLoop after tool dispatch within each iteration.
func notifyLoopIteration(a *Agent, sess *session.Session, toolCalls []ToolUseBlock, iteration int) {
	if a.evolutionBridge == nil {
		return
	}
	toolNames := make([]string, len(toolCalls))
	for i, tc := range toolCalls {
		toolNames[i] = tc.Name
	}
	successCount := countRecentSuccesses(sess, len(toolCalls))
	a.evolutionBridge.OnLoopComplete(sess.ID, len(toolCalls), successCount, iteration, toolNames...)
}

// notifyEpisodeComplete is a shared helper called when the loop finishes.
func notifyEpisodeComplete(a *Agent, sess *session.Session, iteration int, success bool, startTime time.Time) {
	if a.evolutionBridge == nil {
		return
	}
	a.evolutionBridge.OnEpisodeComplete(sess.ID, iteration, success, time.Since(startTime).Milliseconds())
}

// countRecentSuccesses examines the most recent tool_result messages in the
// session history and counts how many represent successful tool executions.
func countRecentSuccesses(sess *session.Session, count int) int {
	msgs := sess.History()
	successes := 0
	remaining := count
	for i := len(msgs) - 1; i >= 0 && remaining > 0; i-- {
		if msgs[i].Role == "tool_result" {
			content := strings.ToLower(msgs[i].Content)
			if !strings.Contains(content, "error:") &&
				!strings.Contains(content, "denied") &&
				!strings.Contains(content, "failed") {
				successes++
			}
			remaining--
		}
	}
	return successes
}
