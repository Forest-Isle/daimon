package cogmetrics

import (
	"testing"
)

func TestBreaker_Evaluate_TriggersCallbacks(t *testing.T) {
	h := NewHealthChecker()
	h.Record("context_utilization", 0.9) // exceeds threshold

	b := NewBreaker(h)
	called := false
	b.OnAction(ActionTriggerCompression, func() {
		called = true
	})

	triggered := b.Evaluate()
	if !called {
		t.Error("expected compression callback to be called")
	}
	if len(triggered) != 1 {
		t.Fatalf("triggered = %d, want 1", len(triggered))
	}
	if triggered[0] != ActionTriggerCompression {
		t.Errorf("triggered action = %v, want ActionTriggerCompression", triggered[0])
	}
}

func TestBreaker_NoViolations(t *testing.T) {
	h := NewHealthChecker()
	h.Record("context_utilization", 0.5) // within threshold

	b := NewBreaker(h)
	called := false
	b.OnAction(ActionTriggerCompression, func() {
		called = true
	})

	triggered := b.Evaluate()
	if called {
		t.Error("callback should not be called when no violations")
	}
	if len(triggered) != 0 {
		t.Errorf("triggered = %d, want 0", len(triggered))
	}
}

func TestBreaker_MultipleViolations(t *testing.T) {
	h := NewHealthChecker()
	h.Record("context_utilization", 0.95) // triggers compression
	h.Record("consecutive_replans", 5)    // triggers degrade

	b := NewBreaker(h)
	compressionCalled := false
	degradeCalled := false
	b.OnAction(ActionTriggerCompression, func() { compressionCalled = true })
	b.OnAction(ActionDegradeToSimple, func() { degradeCalled = true })

	triggered := b.Evaluate()
	if !compressionCalled {
		t.Error("compression callback not called")
	}
	if !degradeCalled {
		t.Error("degrade callback not called")
	}
	if len(triggered) != 2 {
		t.Errorf("triggered = %d, want 2", len(triggered))
	}
}

func TestBreaker_HealthScore(t *testing.T) {
	h := NewHealthChecker()
	b := NewBreaker(h)

	// No metrics recorded = no violations = perfect score
	if score := b.HealthScore(); score != 1.0 {
		t.Errorf("health score = %f, want 1.0", score)
	}

	h.Record("context_utilization", 0.9)
	if score := b.HealthScore(); score >= 1.0 {
		t.Errorf("health score = %f, want < 1.0 after violation", score)
	}
}

func TestBreaker_CallbackWithoutRegistration(t *testing.T) {
	h := NewHealthChecker()
	h.Record("context_utilization", 0.9) // triggers violation

	b := NewBreaker(h)
	// No callback registered — should not panic
	triggered := b.Evaluate()
	if len(triggered) != 1 {
		t.Errorf("triggered = %d, want 1", len(triggered))
	}
}

func TestDefaultHealthRules(t *testing.T) {
	rules := DefaultHealthRules()
	if len(rules) != 6 {
		t.Errorf("default rules count = %d, want 6", len(rules))
	}
}

func TestBreakerAction_String(t *testing.T) {
	tests := []struct {
		action BreakerAction
		want   string
	}{
		{ActionNone, "none"},
		{ActionTriggerCompression, "trigger_compression"},
		{ActionDegradeToSimple, "degrade_to_simple"},
		{ActionPauseAndAskUser, "pause_and_ask_user"},
		{ActionSwitchModel, "switch_model"},
		{ActionDegradeToSyncWrite, "degrade_to_sync_write"},
		{ActionDisableEvolution, "disable_evolution"},
	}
	for _, tt := range tests {
		if got := tt.action.String(); got != tt.want {
			t.Errorf("BreakerAction(%d).String() = %q, want %q", tt.action, got, tt.want)
		}
	}
}

func TestActionFromString_Roundtrip(t *testing.T) {
	actions := []BreakerAction{
		ActionTriggerCompression,
		ActionDegradeToSimple,
		ActionPauseAndAskUser,
		ActionSwitchModel,
		ActionDegradeToSyncWrite,
		ActionDisableEvolution,
	}
	for _, a := range actions {
		if got := actionFromString(a.String()); got != a {
			t.Errorf("roundtrip failed for %v: got %v", a, got)
		}
	}
	if got := actionFromString("unknown"); got != ActionNone {
		t.Errorf("unknown string should map to ActionNone, got %v", got)
	}
}
