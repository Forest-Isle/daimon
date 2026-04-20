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

	bus.Publish(Event{
		Type: EventToolEnd, Timestamp: now, SessionID: "s1",
		Data: map[string]any{"tool_name": "bash", "succeeded": true, "duration_ms": int64(10)},
	})

	bus.Publish(Event{
		Type: EventToolEnd, Timestamp: now, SessionID: "s1",
		Data: map[string]any{"tool_name": "bash", "succeeded": true, "duration_ms": int64(10), "source": "evolution"},
	})

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

func TestStateTrackerMetricsUpdate(t *testing.T) {
	bus := NewBus(16)
	tracker := NewAgentStateTracker(bus)
	go tracker.Run()
	defer tracker.Stop()

	bus.Publish(Event{
		Type: EventMetricsUpdate, Timestamp: time.Now(), SessionID: "s1",
		Data: map[string]any{
			"iteration":      3,
			"max_iterations": 10,
			"utilization":    0.65,
			"input_tokens":   int64(4200),
			"output_tokens":  int64(900),
			"cache_create":   int64(500),
			"cache_read":     int64(300),
			"model":          "claude-sonnet-4-20250514",
			"provider":       "claude",
		},
	})
	time.Sleep(50 * time.Millisecond)

	snap := tracker.Snapshot()
	if len(snap.ActiveSessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(snap.ActiveSessions))
	}
	ss := snap.ActiveSessions[0]
	if ss.Iteration != 3 {
		t.Fatalf("iteration = %d, want 3", ss.Iteration)
	}
	if ss.Utilization != 0.65 {
		t.Fatalf("utilization = %f, want 0.65", ss.Utilization)
	}
	if ss.Model != "claude-sonnet-4-20250514" {
		t.Fatalf("model = %s, want claude-sonnet-4-20250514", ss.Model)
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

func TestStateTrackerSessionEnd(t *testing.T) {
	bus := NewBus(16)
	tracker := NewAgentStateTracker(bus)
	go tracker.Run()
	defer tracker.Stop()

	bus.Publish(Event{
		Type: EventSessionStart, Timestamp: time.Now(), SessionID: "s1",
		Data: map[string]any{"channel": "telegram"},
	})
	time.Sleep(50 * time.Millisecond)

	bus.Publish(Event{
		Type: EventSessionEnd, Timestamp: time.Now(), SessionID: "s1",
		Data: map[string]any{"succeeded": true, "duration_ms": int64(1000)},
	})
	time.Sleep(100 * time.Millisecond)

	snap := tracker.Snapshot()
	if len(snap.ActiveSessions) != 0 {
		t.Fatalf("expected 0 active sessions after end, got %d", len(snap.ActiveSessions))
	}
	if snap.TotalSessions < 1 {
		t.Errorf("TotalSessions = %d, want >= 1", snap.TotalSessions)
	}
}
