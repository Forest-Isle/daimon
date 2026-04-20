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
