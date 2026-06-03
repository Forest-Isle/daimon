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
