package dashboard

import (
	"testing"
	"time"
)

func TestEmitSessionStart(t *testing.T) {
	bus := NewBus(16)
	emitter := NewEmitter(bus)
	ch := bus.Subscribe()

	emitter.EmitSessionStart("s1", "telegram")

	select {
	case ev := <-ch:
		if ev.Type != EventSessionStart {
			t.Fatalf("type = %s, want session.start", ev.Type)
		}
		if ev.SessionID != "s1" {
			t.Errorf("session_id = %s, want s1", ev.SessionID)
		}
		if ev.Data["channel"] != "telegram" {
			t.Errorf("channel = %v, want telegram", ev.Data["channel"])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for session.start event")
	}
}

func TestEmitSessionEnd(t *testing.T) {
	bus := NewBus(16)
	emitter := NewEmitter(bus)
	ch := bus.Subscribe()

	emitter.EmitSessionEnd("s1", true, 500)

	select {
	case ev := <-ch:
		if ev.Type != EventSessionEnd {
			t.Fatalf("type = %s, want session.end", ev.Type)
		}
		if ev.SessionID != "s1" {
			t.Errorf("session_id = %s, want s1", ev.SessionID)
		}
		if ev.Data["succeeded"] != true {
			t.Errorf("succeeded = %v, want true", ev.Data["succeeded"])
		}
		if ev.Data["duration_ms"] != int64(500) {
			t.Errorf("duration_ms = %v, want 500", ev.Data["duration_ms"])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for session.end event")
	}
}

func TestEmitterMetricsUpdate(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	em := NewEmitter(bus)
	em.EmitMetricsUpdate("s1", 3, 10, 0.72, 5000, 1200, 800, 600, "claude-sonnet-4-20250514", "claude")

	select {
	case ev := <-ch:
		if ev.Type != EventMetricsUpdate {
			t.Fatalf("type = %s, want metrics.update", ev.Type)
		}
		if ev.SessionID != "s1" {
			t.Fatalf("session = %s, want s1", ev.SessionID)
		}
		if ev.Data["iteration"] != 3 {
			t.Fatalf("iteration = %v, want 3", ev.Data["iteration"])
		}
		if ev.Data["utilization"] != 0.72 {
			t.Fatalf("utilization = %v, want 0.72", ev.Data["utilization"])
		}
		if ev.Data["input_tokens"] != int64(5000) {
			t.Fatalf("input_tokens = %v, want 5000", ev.Data["input_tokens"])
		}
		if ev.Data["model"] != "claude-sonnet-4-20250514" {
			t.Fatalf("model = %v, want claude-sonnet-4-20250514", ev.Data["model"])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestEmitterTruncatesLongInput(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	em := NewEmitter(bus)
	longInput := make([]byte, 1000)
	for i := range longInput {
		longInput[i] = 'a'
	}
	em.EmitToolStart("s1", "bash", string(longInput))

	select {
	case ev := <-ch:
		input := ev.Data["input"].(string)
		if len(input) > 503 {
			t.Fatalf("input length = %d, want <= 503", len(input))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}
