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
		if len(input) > 503 {
			t.Fatalf("input length = %d, want <= 503", len(input))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestEmitterSubAgentSpawn(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	em := NewEmitter(bus)
	em.EmitSubAgentSpawn("sub1", "parent1", "researcher", "find relevant docs")

	select {
	case ev := <-ch:
		if ev.Type != EventSubAgentSpawn {
			t.Fatalf("type = %s, want subagent.spawn", ev.Type)
		}
		if ev.SessionID != "sub1" {
			t.Fatalf("session = %s, want sub1", ev.SessionID)
		}
		if ev.Data["parent_session_id"] != "parent1" {
			t.Fatalf("parent = %v, want parent1", ev.Data["parent_session_id"])
		}
		if ev.Data["agent_name"] != "researcher" {
			t.Fatalf("agent = %v, want researcher", ev.Data["agent_name"])
		}
		if ev.Data["task"] != "find relevant docs" {
			t.Fatalf("task = %v, want 'find relevant docs'", ev.Data["task"])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestEmitterSubAgentSpawnTruncatesTask(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	em := NewEmitter(bus)
	longTask := make([]byte, 300)
	for i := range longTask {
		longTask[i] = 'x'
	}
	em.EmitSubAgentSpawn("sub1", "parent1", "agent", string(longTask))

	select {
	case ev := <-ch:
		task := ev.Data["task"].(string)
		if len(task) > 203 { // 200 + "..."
			t.Fatalf("task length = %d, want <= 203", len(task))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestEmitterSubAgentComplete(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	em := NewEmitter(bus)
	em.EmitSubAgentComplete("sub1", "researcher", true, 1500)

	select {
	case ev := <-ch:
		if ev.Type != EventSubAgentComplete {
			t.Fatalf("type = %s, want subagent.complete", ev.Type)
		}
		if ev.Data["agent_name"] != "researcher" {
			t.Fatalf("agent = %v, want researcher", ev.Data["agent_name"])
		}
		if ev.Data["succeeded"] != true {
			t.Fatalf("succeeded = %v, want true", ev.Data["succeeded"])
		}
		if ev.Data["duration_ms"] != int64(1500) {
			t.Fatalf("duration = %v, want 1500", ev.Data["duration_ms"])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestEmitterContextCompress(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	em := NewEmitter(bus)
	em.EmitContextCompress("s1", "proactive", 3, 0.85, 0.42)

	select {
	case ev := <-ch:
		if ev.Type != EventContextCompress {
			t.Fatalf("type = %s, want context.compress", ev.Type)
		}
		if ev.Data["reason"] != "proactive" {
			t.Fatalf("reason = %v, want proactive", ev.Data["reason"])
		}
		if ev.Data["layers_run"] != 3 {
			t.Fatalf("layers_run = %v, want 3", ev.Data["layers_run"])
		}
		if ev.Data["before_pct"] != 0.85 {
			t.Fatalf("before_pct = %v, want 0.85", ev.Data["before_pct"])
		}
		if ev.Data["after_pct"] != 0.42 {
			t.Fatalf("after_pct = %v, want 0.42", ev.Data["after_pct"])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}
