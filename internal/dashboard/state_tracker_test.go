package dashboard

import (
	"testing"
	"time"
)

func TestStateTrackerPhaseTransition(t *testing.T) {
	bus := NewBus(16)
	tracker := NewAgentStateTracker(bus)
	go tracker.Run()
	defer tracker.Stop()

	bus.Publish(Event{
		Type: EventPhaseStart, Timestamp: time.Now(), SessionID: "s1",
		Data: map[string]any{"phase": "PLAN"},
	})
	time.Sleep(50 * time.Millisecond)

	snap := tracker.Snapshot()
	if snap.Status != "busy" {
		t.Fatalf("status = %s, want busy", snap.Status)
	}
	if len(snap.ActiveSessions) != 1 {
		t.Fatalf("active sessions = %d, want 1", len(snap.ActiveSessions))
	}
	if snap.ActiveSessions[0].CurrentPhase != "PLAN" {
		t.Fatalf("phase = %s, want PLAN", snap.ActiveSessions[0].CurrentPhase)
	}
}

func TestStateTrackerToolExecution(t *testing.T) {
	bus := NewBus(16)
	tracker := NewAgentStateTracker(bus)
	go tracker.Run()
	defer tracker.Stop()

	bus.Publish(Event{
		Type: EventToolStart, Timestamp: time.Now(), SessionID: "s1",
		Data: map[string]any{"tool_name": "bash"},
	})
	time.Sleep(50 * time.Millisecond)

	snap := tracker.Snapshot()
	if len(snap.ActiveSessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(snap.ActiveSessions))
	}
	if snap.ActiveSessions[0].CurrentTool != "bash" {
		t.Fatalf("tool = %s, want bash", snap.ActiveSessions[0].CurrentTool)
	}

	bus.Publish(Event{
		Type: EventToolEnd, Timestamp: time.Now(), SessionID: "s1",
		Data: map[string]any{"tool_name": "bash", "succeeded": true},
	})
	time.Sleep(50 * time.Millisecond)

	snap = tracker.Snapshot()
	if snap.ActiveSessions[0].CurrentTool != "" {
		t.Fatalf("tool should be cleared, got %s", snap.ActiveSessions[0].CurrentTool)
	}
	if snap.ActiveSessions[0].ToolsExecuted != 1 {
		t.Fatalf("tools_executed = %d, want 1", snap.ActiveSessions[0].ToolsExecuted)
	}
}

func TestStateTrackerSessionEnd(t *testing.T) {
	bus := NewBus(16)
	tracker := NewAgentStateTracker(bus)
	go tracker.Run()
	defer tracker.Stop()

	bus.Publish(Event{
		Type: EventPhaseStart, Timestamp: time.Now(), SessionID: "s1",
		Data: map[string]any{"phase": "PERCEIVE"},
	})
	time.Sleep(50 * time.Millisecond)

	bus.Publish(Event{
		Type: EventSessionEnd, Timestamp: time.Now(), SessionID: "s1",
		Data: map[string]any{},
	})
	time.Sleep(50 * time.Millisecond)

	snap := tracker.Snapshot()
	if snap.Status != "idle" {
		t.Fatalf("status = %s, want idle", snap.Status)
	}
	if len(snap.ActiveSessions) != 0 {
		t.Fatalf("sessions = %d, want 0", len(snap.ActiveSessions))
	}
	if snap.TotalSessions != 1 {
		t.Fatalf("total = %d, want 1", snap.TotalSessions)
	}
}
