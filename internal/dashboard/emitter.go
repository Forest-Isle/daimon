package dashboard

import "time"

const maxInputLen = 500

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
