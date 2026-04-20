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
		if ev.Data["max_iterations"] != 10 {
			t.Fatalf("max_iterations = %v, want 10", ev.Data["max_iterations"])
		}
		if ev.Data["utilization"] != 0.72 {
			t.Fatalf("utilization = %v, want 0.72", ev.Data["utilization"])
		}
		if ev.Data["input_tokens"] != int64(5000) {
			t.Fatalf("input_tokens = %v, want 5000", ev.Data["input_tokens"])
		}
		if ev.Data["output_tokens"] != int64(1200) {
			t.Fatalf("output_tokens = %v, want 1200", ev.Data["output_tokens"])
		}
		if ev.Data["cache_create"] != int64(800) {
			t.Fatalf("cache_create = %v, want 800", ev.Data["cache_create"])
		}
		if ev.Data["cache_read"] != int64(600) {
			t.Fatalf("cache_read = %v, want 600", ev.Data["cache_read"])
		}
		if ev.Data["model"] != "claude-sonnet-4-20250514" {
			t.Fatalf("model = %v, want claude-sonnet-4-20250514", ev.Data["model"])
		}
		if ev.Data["provider"] != "claude" {
			t.Fatalf("provider = %v, want claude", ev.Data["provider"])
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
