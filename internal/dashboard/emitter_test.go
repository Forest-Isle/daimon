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
