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
	if ss.MaxIter != 10 {
		t.Fatalf("max_iterations = %d, want 10", ss.MaxIter)
	}
	if ss.Utilization != 0.65 {
		t.Fatalf("utilization = %f, want 0.65", ss.Utilization)
	}
	if ss.InputTokens != 4200 {
		t.Fatalf("input_tokens = %d, want 4200", ss.InputTokens)
	}
	if ss.OutputTokens != 900 {
		t.Fatalf("output_tokens = %d, want 900", ss.OutputTokens)
	}
	if ss.CacheCreate != 500 {
		t.Fatalf("cache_create = %d, want 500", ss.CacheCreate)
	}
	if ss.CacheRead != 300 {
		t.Fatalf("cache_read = %d, want 300", ss.CacheRead)
	}
	if ss.Model != "claude-sonnet-4-20250514" {
		t.Fatalf("model = %s, want claude-sonnet-4-20250514", ss.Model)
	}
	if ss.Provider != "claude" {
		t.Fatalf("provider = %s, want claude", ss.Provider)
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
