package dashboard

import (
	"testing"
	"time"
)

func TestEmitterPhaseStart(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	em := NewEmitter(bus)
	em.EmitPhaseStart("s1", "PLAN")

	select {
	case ev := <-ch:
		if ev.Type != EventPhaseStart {
			t.Fatalf("type = %s, want phase.start", ev.Type)
		}
		if ev.SessionID != "s1" {
			t.Fatalf("session = %s, want s1", ev.SessionID)
		}
		if ev.Data["phase"] != "PLAN" {
			t.Fatalf("phase = %v, want PLAN", ev.Data["phase"])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestEmitterToolEndWithDuration(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	em := NewEmitter(bus)
	em.EmitToolEnd("s1", "bash", true, 250)

	select {
	case ev := <-ch:
		if ev.Type != EventToolEnd {
			t.Fatalf("type = %s, want tool.end", ev.Type)
		}
		if ev.Data["tool_name"] != "bash" {
			t.Fatalf("tool = %v, want bash", ev.Data["tool_name"])
		}
		if ev.Data["succeeded"] != true {
			t.Fatalf("succeeded = %v, want true", ev.Data["succeeded"])
		}
		if ev.Data["duration_ms"] != int64(250) {
			t.Fatalf("duration = %v, want 250", ev.Data["duration_ms"])
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
		if len(input) > 503 { // 500 + "..."
			t.Fatalf("input length = %d, want <= 503", len(input))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}
