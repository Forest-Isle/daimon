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
