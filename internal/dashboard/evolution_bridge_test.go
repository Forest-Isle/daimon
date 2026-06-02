package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/evolution"
)

func TestEvolutionBridgeSourceField(t *testing.T) {
	bus := NewBus(16)
	bridge := NewEvolutionBridge(bus)
	ch := bus.Subscribe()

	now := time.Now()

	bridge.OnToolExecuted(context.Background(), evolution.ToolExecEvent{
		SessionID:  "s1",
		ToolName:   "bash",
		Succeeded:  true,
		DurationMs: 42,
		Timestamp:  now,
	})

	bridge.OnReflectionComplete(context.Background(), evolution.ReflectionEvent{
		SessionID:  "s1",
		Succeeded:  true,
		Confidence: 0.9,
		Timestamp:  now,
	})

	bridge.OnEpisodeComplete(context.Background(), evolution.EpisodeEvent{
		SessionID:   "s1",
		Succeeded:   true,
		DurationMs:  100,
		ReplanCount: 1,
		Timestamp:   now,
	})

	events := drainEvents(ch, 3)

	for _, ev := range events {
		src, ok := ev.Data["source"]
		if !ok {
			t.Errorf("event %s missing source field", ev.Type)
			continue
		}
		if src != "evolution" {
			t.Errorf("event %s source = %v, want evolution", ev.Type, src)
		}
	}

	if events[0].Type != EventToolEnd {
		t.Errorf("first event type = %s, want tool.end", events[0].Type)
	}
	if events[1].Type != EventPhaseEnd {
		t.Errorf("second event type = %s, want phase.end", events[1].Type)
	}
	if events[2].Type != EventSessionEnd {
		t.Errorf("third event type = %s, want session.end", events[2].Type)
	}
}

func drainEvents(ch chan Event, n int) []Event {
	events := make([]Event, 0, n)
	timeout := time.After(time.Second)
	for len(events) < n {
		select {
		case ev := <-ch:
			events = append(events, ev)
		case <-timeout:
			return events
		}
	}
	return events
}
