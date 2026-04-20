package dashboard

import (
	"context"

	"github.com/Forest-Isle/IronClaw/internal/evolution"
)

// EvolutionBridge adapts evolution.Hook events to the dashboard Event Bus.
type EvolutionBridge struct {
	bus *Bus
}

func NewEvolutionBridge(bus *Bus) *EvolutionBridge {
	return &EvolutionBridge{bus: bus}
}

func (b *EvolutionBridge) Name() string { return "dashboard_bridge" }

func (b *EvolutionBridge) OnReflectionComplete(_ context.Context, event evolution.ReflectionEvent) {
	b.bus.Publish(Event{
		Type:      EventPhaseEnd,
		Timestamp: event.Timestamp,
		SessionID: event.SessionID,
		Data: map[string]any{
			"phase":      "REFLECT",
			"succeeded":  event.Succeeded,
			"confidence": event.Confidence,
			"source":     "evolution",
		},
	})
}

func (b *EvolutionBridge) OnEpisodeComplete(_ context.Context, event evolution.EpisodeEvent) {
	b.bus.Publish(Event{
		Type:      EventSessionEnd,
		Timestamp: event.Timestamp,
		SessionID: event.SessionID,
		Data: map[string]any{
			"succeeded":    event.Succeeded,
			"duration_ms":  event.DurationMs,
			"replan_count": event.ReplanCount,
			"source":       "evolution",
		},
	})
}

func (b *EvolutionBridge) OnToolExecuted(_ context.Context, event evolution.ToolExecEvent) {
	b.bus.Publish(Event{
		Type:      EventToolEnd,
		Timestamp: event.Timestamp,
		SessionID: event.SessionID,
		Data: map[string]any{
			"tool_name":   event.ToolName,
			"succeeded":   event.Succeeded,
			"duration_ms": event.DurationMs,
			"source":      "evolution",
		},
	})
}

var _ evolution.Hook = (*EvolutionBridge)(nil)
