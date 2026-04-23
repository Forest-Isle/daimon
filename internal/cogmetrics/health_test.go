package cogmetrics

import (
	"math"
	"testing"
)

func TestMetricWindow_Add_Average_Last(t *testing.T) {
	w := NewMetricWindow(3)
	w.Add(1.0)
	w.Add(2.0)
	w.Add(3.0)

	if w.Count() != 3 {
		t.Errorf("count = %d, want 3", w.Count())
	}
	if got := w.Average(); math.Abs(got-2.0) > 1e-9 {
		t.Errorf("average = %f, want 2.0", got)
	}
	if got := w.Last(); got != 3.0 {
		t.Errorf("last = %f, want 3.0", got)
	}

	// Overflow: adding 4th value evicts the first
	w.Add(4.0)
	if w.Count() != 3 {
		t.Errorf("count after overflow = %d, want 3", w.Count())
	}
	if got := w.Average(); math.Abs(got-3.0) > 1e-9 {
		t.Errorf("average after overflow = %f, want 3.0", got)
	}
	if got := w.Last(); got != 4.0 {
		t.Errorf("last after overflow = %f, want 4.0", got)
	}
}

func TestMetricWindow_Empty(t *testing.T) {
	w := NewMetricWindow(10)
	if w.Average() != 0 {
		t.Errorf("empty average = %f, want 0", w.Average())
	}
	if w.Last() != 0 {
		t.Errorf("empty last = %f, want 0", w.Last())
	}
	if w.Count() != 0 {
		t.Errorf("empty count = %d, want 0", w.Count())
	}
}

func TestHealthChecker_Record_Check(t *testing.T) {
	h := NewHealthChecker()
	h.Record("context_utilization", 0.5)
	h.Record("context_utilization", 0.6)

	status := h.Check()
	if status.Score != 1.0 {
		t.Errorf("score = %f, want 1.0 (no violations)", status.Score)
	}
	if len(status.Violations) != 0 {
		t.Errorf("violations = %d, want 0", len(status.Violations))
	}
	if v, ok := status.Indicators["context_utilization"]; !ok || v != 0.6 {
		t.Errorf("indicator context_utilization = %f, want 0.6", v)
	}
}

func TestHealthChecker_NoViolations(t *testing.T) {
	h := NewHealthChecker()
	// All metrics well within thresholds
	h.Record("context_utilization", 0.3)
	h.Record("consecutive_replans", 1)

	// reflect_confidence above threshold (good)
	for i := 0; i < 3; i++ {
		h.Record("reflect_confidence", 0.8)
	}

	status := h.Check()
	if status.Score != 1.0 {
		t.Errorf("score = %f, want 1.0", status.Score)
	}
	if len(status.Violations) != 0 {
		t.Errorf("violations = %d, want 0", len(status.Violations))
	}
}

func TestHealthChecker_ContextUtilization(t *testing.T) {
	h := NewHealthChecker()
	h.Record("context_utilization", 0.9) // exceeds 0.85 threshold

	status := h.Check()
	if len(status.Violations) != 1 {
		t.Fatalf("violations = %d, want 1", len(status.Violations))
	}
	v := status.Violations[0]
	if v.Metric != "context_utilization" {
		t.Errorf("violation metric = %s, want context_utilization", v.Metric)
	}
	if v.Action != "trigger_compression" {
		t.Errorf("violation action = %s, want trigger_compression", v.Action)
	}
	// Score should be 1.0 - 0.15 = 0.85
	if math.Abs(status.Score-0.85) > 1e-9 {
		t.Errorf("score = %f, want 0.85", status.Score)
	}
}

func TestHealthChecker_LowConfidence(t *testing.T) {
	h := NewHealthChecker()
	// Need at least 3 samples (MinSamples = 3)
	h.Record("reflect_confidence", 0.2)
	h.Record("reflect_confidence", 0.1)
	h.Record("reflect_confidence", 0.25)

	status := h.Check()
	found := false
	for _, v := range status.Violations {
		if v.Metric == "reflect_confidence" {
			found = true
			if v.Action != "switch_model" {
				t.Errorf("action = %s, want switch_model", v.Action)
			}
		}
	}
	if !found {
		t.Error("expected reflect_confidence violation, not found")
	}
}

func TestHealthChecker_MinSamplesNotMet(t *testing.T) {
	h := NewHealthChecker()
	// tool_failure_rate needs MinSamples=5, only provide 2
	h.Record("tool_failure_rate", 0.9)
	h.Record("tool_failure_rate", 0.8)

	status := h.Check()
	for _, v := range status.Violations {
		if v.Metric == "tool_failure_rate" {
			t.Error("tool_failure_rate should not trigger with < 5 samples")
		}
	}
}

func TestHealthChecker_MultipleViolations(t *testing.T) {
	h := NewHealthChecker()
	h.Record("context_utilization", 0.95)  // triggers compression
	h.Record("consecutive_replans", 5)     // triggers degrade

	status := h.Check()
	if len(status.Violations) != 2 {
		t.Fatalf("violations = %d, want 2", len(status.Violations))
	}
	// Score = 1.0 - 0.15 - 0.25 = 0.60
	if math.Abs(status.Score-0.60) > 1e-9 {
		t.Errorf("score = %f, want 0.60", status.Score)
	}
}
