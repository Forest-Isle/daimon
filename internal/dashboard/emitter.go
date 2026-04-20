package dashboard

import "time"

const maxInputLen = 500
const maxTaskLen = 200

type Emitter struct {
	bus *Bus
}

func NewEmitter(bus *Bus) *Emitter {
	return &Emitter{bus: bus}
}

func (e *Emitter) EmitPhaseStart(sessionID, phase string) {
	e.bus.Publish(Event{
		Type:      EventPhaseStart,
		Timestamp: time.Now(),
		SessionID: sessionID,
		Data:      map[string]any{"phase": phase},
	})
}

func (e *Emitter) EmitPhaseEnd(sessionID, phase string, durationMs int64) {
	e.bus.Publish(Event{
		Type:      EventPhaseEnd,
		Timestamp: time.Now(),
		SessionID: sessionID,
		Data:      map[string]any{"phase": phase, "duration_ms": durationMs},
	})
}

func (e *Emitter) EmitToolStart(sessionID, toolName, input string) {
	if len(input) > maxInputLen {
		input = input[:maxInputLen] + "..."
	}
	e.bus.Publish(Event{
		Type:      EventToolStart,
		Timestamp: time.Now(),
		SessionID: sessionID,
		Data:      map[string]any{"tool_name": toolName, "input": input},
	})
}

func (e *Emitter) EmitToolEnd(sessionID, toolName string, succeeded bool, durationMs int64) {
	e.bus.Publish(Event{
		Type:      EventToolEnd,
		Timestamp: time.Now(),
		SessionID: sessionID,
		Data: map[string]any{
			"tool_name":   toolName,
			"succeeded":   succeeded,
			"duration_ms": durationMs,
		},
	})
}

func (e *Emitter) EmitSessionStart(sessionID, channel string) {
	e.bus.Publish(Event{
		Type:      EventSessionStart,
		Timestamp: time.Now(),
		SessionID: sessionID,
		Data:      map[string]any{"channel": channel},
	})
}

func (e *Emitter) EmitSessionEnd(sessionID string, succeeded bool, durationMs int64) {
	e.bus.Publish(Event{
		Type:      EventSessionEnd,
		Timestamp: time.Now(),
		SessionID: sessionID,
		Data: map[string]any{
			"succeeded":   succeeded,
			"duration_ms": durationMs,
		},
	})
}

func (e *Emitter) EmitPlanGenerated(sessionID string, taskCount int, complexity string, hasDirectReply bool) {
	e.bus.Publish(Event{
		Type:      EventPlanGenerated,
		Timestamp: time.Now(),
		SessionID: sessionID,
		Data: map[string]any{
			"task_count":       taskCount,
			"complexity":       complexity,
			"has_direct_reply": hasDirectReply,
		},
	})
}

func (e *Emitter) EmitMetricsUpdate(sessionID string, iteration, maxIter int, utilization float64, inputTokens, outputTokens, cacheCreate, cacheRead int64, model, provider string) {
	e.bus.Publish(Event{
		Type:      EventMetricsUpdate,
		Timestamp: time.Now(),
		SessionID: sessionID,
		Data: map[string]any{
			"iteration":      iteration,
			"max_iterations": maxIter,
			"utilization":    utilization,
			"input_tokens":   inputTokens,
			"output_tokens":  outputTokens,
			"cache_create":   cacheCreate,
			"cache_read":     cacheRead,
			"model":          model,
			"provider":       provider,
		},
	})
}

func (e *Emitter) EmitReplanStart(sessionID string, attempt int, reason string) {
	e.bus.Publish(Event{
		Type:      EventReplanStart,
		Timestamp: time.Now(),
		SessionID: sessionID,
		Data: map[string]any{
			"attempt": attempt,
			"reason":  reason,
		},
	})
}

func (e *Emitter) EmitObservationResult(sessionID string, passed, failed, total int, overallProgress float64) {
	e.bus.Publish(Event{
		Type:      EventObservationResult,
		Timestamp: time.Now(),
		SessionID: sessionID,
		Data: map[string]any{
			"passed":           passed,
			"failed":           failed,
			"total":            total,
			"overall_progress": overallProgress,
		},
	})
}

func (e *Emitter) EmitSubAgentSpawn(sessionID, parentSessionID, agentName, task string) {
	if len(task) > maxTaskLen {
		task = task[:maxTaskLen] + "..."
	}
	e.bus.Publish(Event{
		Type:      EventSubAgentSpawn,
		Timestamp: time.Now(),
		SessionID: sessionID,
		Data: map[string]any{
			"parent_session_id": parentSessionID,
			"agent_name":        agentName,
			"task":              task,
		},
	})
}

func (e *Emitter) EmitSubAgentComplete(sessionID, agentName string, succeeded bool, durationMs int64) {
	e.bus.Publish(Event{
		Type:      EventSubAgentComplete,
		Timestamp: time.Now(),
		SessionID: sessionID,
		Data: map[string]any{
			"agent_name":  agentName,
			"succeeded":   succeeded,
			"duration_ms": durationMs,
		},
	})
}

func (e *Emitter) EmitContextCompress(sessionID, reason string, layersRun int, beforePct, afterPct float64) {
	e.bus.Publish(Event{
		Type:      EventContextCompress,
		Timestamp: time.Now(),
		SessionID: sessionID,
		Data: map[string]any{
			"reason":     reason,
			"layers_run": layersRun,
			"before_pct": beforePct,
			"after_pct":  afterPct,
		},
	})
}
