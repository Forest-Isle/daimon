package memory

import (
	"testing"
	"time"
)

func TestSectionBuffer_AddAndShouldUpdate(t *testing.T) {
	sec := ProfileSection{
		ID: "communication", Priority: 0,
		FactThreshold: 3, TimeThreshold: 1 * time.Hour,
	}
	buf := NewSectionBuffer(sec)

	buf.Add("fact 1")
	buf.Add("fact 2")
	if buf.ShouldUpdate() {
		t.Error("should not trigger with only 2 facts")
	}

	buf.Add("fact 3")
	if !buf.ShouldUpdate() {
		t.Error("should trigger with 3 facts (threshold=3)")
	}
}

func TestSectionBuffer_TimeThreshold(t *testing.T) {
	sec := ProfileSection{
		ID: "communication", Priority: 0,
		FactThreshold: 100, TimeThreshold: 50 * time.Millisecond,
	}
	buf := NewSectionBuffer(sec)
	buf.Add("fact 1")

	if buf.ShouldUpdate() {
		t.Error("should not trigger yet")
	}

	buf.lastUpdated = time.Now().Add(-100 * time.Millisecond)
	if !buf.ShouldUpdate() {
		t.Error("should trigger after time threshold")
	}
}

func TestSectionBuffer_Drain(t *testing.T) {
	sec := ProfileSection{
		ID: "tech_stack", Priority: 0,
		FactThreshold: 3, TimeThreshold: 1 * time.Hour,
	}
	buf := NewSectionBuffer(sec)
	buf.Add("fact A")
	buf.Add("fact B")
	buf.Add("fact C")

	facts := buf.Drain()
	if len(facts) != 3 {
		t.Fatalf("expected 3 drained facts, got %d", len(facts))
	}
	if facts[0] != "fact A" {
		t.Errorf("first fact: want %q, got %q", "fact A", facts[0])
	}

	if len(buf.pending) != 0 {
		t.Errorf("buffer should be empty after drain, got %d", len(buf.pending))
	}
	if buf.ShouldUpdate() {
		t.Error("should not trigger after drain")
	}
}
