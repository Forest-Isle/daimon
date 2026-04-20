package dashboard

import (
	"testing"
	"time"
)

func TestStateTrackerSkipsEvolutionToolEnd(t *testing.T) {
	bus := NewBus(16)
	tracker := NewAgentStateTracker(bus)
	go tracker.Run()
	defer tracker.Stop()

	now := time.Now()

	// Primary tool.end from Emitter (no source field)
	bus.Publish(Event{
		Type: EventToolEnd, Timestamp: now, SessionID: "s1",
		Data: map[string]any{"tool_name": "bash", "succeeded": true, "duration_ms": int64(10)},
	})

	// Duplicate tool.end from EvolutionBridge (source=evolution)
	bus.Publish(Event{
		Type: EventToolEnd, Timestamp: now, SessionID: "s1",
		Data: map[string]any{"tool_name": "bash", "succeeded": true, "duration_ms": int64(10), "source": "evolution"},
	})

	// Allow goroutine to process
	time.Sleep(50 * time.Millisecond)

	snap := tracker.Snapshot()
	if len(snap.ActiveSessions) != 1 {
		t.Fatalf("expected 1 active session, got %d", len(snap.ActiveSessions))
	}
	if snap.ActiveSessions[0].ToolsExecuted != 1 {
		t.Errorf("ToolsExecuted = %d, want 1 (evolution source should be skipped)", snap.ActiveSessions[0].ToolsExecuted)
	}
}

func TestStateTrackerSkipsEvolutionPhaseEnd(t *testing.T) {
	bus := NewBus(16)
	tracker := NewAgentStateTracker(bus)
	go tracker.Run()
	defer tracker.Stop()

	now := time.Now()

	bus.Publish(Event{
		Type: EventPhaseStart, Timestamp: now, SessionID: "s1",
		Data: map[string]any{"phase": "REFLECT"},
	})

	// Evolution-sourced phase.end should NOT clear CurrentPhase
	bus.Publish(Event{
		Type: EventPhaseEnd, Timestamp: now, SessionID: "s1",
		Data: map[string]any{"phase": "REFLECT", "source": "evolution"},
	})

	time.Sleep(50 * time.Millisecond)

	snap := tracker.Snapshot()
	if len(snap.ActiveSessions) != 1 {
		t.Fatalf("expected 1 active session, got %d", len(snap.ActiveSessions))
	}
	if snap.ActiveSessions[0].CurrentPhase != "REFLECT" {
		t.Errorf("CurrentPhase = %q, want REFLECT (evolution source should not clear)", snap.ActiveSessions[0].CurrentPhase)
	}
}

func TestStateTrackerSessionStartSetsChannel(t *testing.T) {
	bus := NewBus(16)
	tracker := NewAgentStateTracker(bus)
	go tracker.Run()
	defer tracker.Stop()

	bus.Publish(Event{
		Type: EventSessionStart, Timestamp: time.Now(), SessionID: "s1",
		Data: map[string]any{"channel": "telegram"},
	})

	time.Sleep(50 * time.Millisecond)

	snap := tracker.Snapshot()
	if len(snap.ActiveSessions) != 1 {
		t.Fatalf("expected 1 active session, got %d", len(snap.ActiveSessions))
	}
	if snap.ActiveSessions[0].Channel != "telegram" {
		t.Errorf("Channel = %q, want telegram", snap.ActiveSessions[0].Channel)
	}
}
