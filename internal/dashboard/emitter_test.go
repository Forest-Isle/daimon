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

func TestEmitterPlanGenerated(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	em := NewEmitter(bus)
	em.EmitPlanGenerated("s1", 5, "complex", false)

	select {
	case ev := <-ch:
		if ev.Type != EventPlanGenerated {
			t.Fatalf("type = %s, want plan.generated", ev.Type)
		}
		if ev.SessionID != "s1" {
			t.Fatalf("session = %s, want s1", ev.SessionID)
		}
		if ev.Data["task_count"] != 5 {
			t.Fatalf("task_count = %v, want 5", ev.Data["task_count"])
		}
		if ev.Data["complexity"] != "complex" {
			t.Fatalf("complexity = %v, want complex", ev.Data["complexity"])
		}
		if ev.Data["has_direct_reply"] != false {
			t.Fatalf("has_direct_reply = %v, want false", ev.Data["has_direct_reply"])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestEmitterReplanStart(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	em := NewEmitter(bus)
	em.EmitReplanStart("s1", 1, "low_confidence")

	select {
	case ev := <-ch:
		if ev.Type != EventReplanStart {
			t.Fatalf("type = %s, want replan.start", ev.Type)
		}
		if ev.Data["attempt"] != 1 {
			t.Fatalf("attempt = %v, want 1", ev.Data["attempt"])
		}
		if ev.Data["reason"] != "low_confidence" {
			t.Fatalf("reason = %v, want low_confidence", ev.Data["reason"])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestEmitterObservationResult(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	em := NewEmitter(bus)
	em.EmitObservationResult("s1", 3, 1, 4, 0.75)

	select {
	case ev := <-ch:
		if ev.Type != EventObservationResult {
			t.Fatalf("type = %s, want observation.result", ev.Type)
		}
		if ev.Data["passed"] != 3 {
			t.Fatalf("passed = %v, want 3", ev.Data["passed"])
		}
		if ev.Data["failed"] != 1 {
			t.Fatalf("failed = %v, want 1", ev.Data["failed"])
		}
		if ev.Data["total"] != 4 {
			t.Fatalf("total = %v, want 4", ev.Data["total"])
		}
		if ev.Data["overall_progress"] != 0.75 {
			t.Fatalf("progress = %v, want 0.75", ev.Data["overall_progress"])
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
