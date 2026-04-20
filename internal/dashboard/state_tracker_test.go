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

func TestStateTrackerSubAgentLifecycle(t *testing.T) {
	bus := NewBus(16)
	tracker := NewAgentStateTracker(bus)
	go tracker.Run()
	defer tracker.Stop()

	bus.Publish(Event{
		Type: EventSubAgentSpawn, Timestamp: time.Now(), SessionID: "sub1",
		Data: map[string]any{
			"parent_session_id": "parent1",
			"agent_name":        "researcher",
			"task":              "find docs",
		},
	})
	time.Sleep(50 * time.Millisecond)

	snap := tracker.Snapshot()
	if len(snap.ActiveSubAgents) != 1 {
		t.Fatalf("active sub-agents = %d, want 1", len(snap.ActiveSubAgents))
	}
	sa := snap.ActiveSubAgents[0]
	if sa.SessionID != "sub1" {
		t.Fatalf("session = %s, want sub1", sa.SessionID)
	}
	if sa.ParentSessionID != "parent1" {
		t.Fatalf("parent = %s, want parent1", sa.ParentSessionID)
	}
	if sa.AgentName != "researcher" {
		t.Fatalf("agent = %s, want researcher", sa.AgentName)
	}
	if sa.Task != "find docs" {
		t.Fatalf("task = %s, want 'find docs'", sa.Task)
	}

	bus.Publish(Event{
		Type: EventSubAgentComplete, Timestamp: time.Now(), SessionID: "sub1",
		Data: map[string]any{"agent_name": "researcher", "succeeded": true, "duration_ms": int64(500)},
	})
	time.Sleep(50 * time.Millisecond)

	snap = tracker.Snapshot()
	if len(snap.ActiveSubAgents) != 0 {
		t.Fatalf("active sub-agents after complete = %d, want 0", len(snap.ActiveSubAgents))
	}
}

func TestStateTrackerCompressionCount(t *testing.T) {
	bus := NewBus(16)
	tracker := NewAgentStateTracker(bus)
	go tracker.Run()
	defer tracker.Stop()

	for i := 0; i < 3; i++ {
		bus.Publish(Event{
			Type: EventContextCompress, Timestamp: time.Now(), SessionID: "s1",
			Data: map[string]any{"reason": "proactive", "layers_run": 5, "before_pct": 0.9, "after_pct": 0.4},
		})
	}
	time.Sleep(50 * time.Millisecond)

	snap := tracker.Snapshot()
	if snap.CompressionEvents != 3 {
		t.Fatalf("compression events = %d, want 3", snap.CompressionEvents)
	}
}
