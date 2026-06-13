package attention

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/Forest-Isle/daimon/internal/heart"
)

// TestHardWhitelistOverridesRules verifies a high-risk kind always wakes the
// user even when a (mistaken or malicious synthesized) rule says to ignore it.
func TestHardWhitelistOverridesRules(t *testing.T) {
	rules := NewRulesRouter([]Rule{
		{Kind: "payment.charge", Action: "ignore"}, // must NOT win
	})
	chain := NewChain(rules, nil)
	chain.SetHighRiskKinds(DefaultHighRiskKinds())

	v, err := chain.Route(context.Background(), heart.Event{Source: "bank", Kind: "payment.charge"})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if v.Action != WakeUser {
		t.Fatalf("action = %v, want WakeUser (hard whitelist must override ignore rule)", v.Action)
	}
}

// TestHighRiskPrefixMatch verifies prefix matching covers kind families.
func TestHighRiskPrefixMatch(t *testing.T) {
	chain := NewChain(nil, nil)
	chain.SetHighRiskKinds([]string{"payment."})
	for _, kind := range []string{"payment.charge", "payment.refund", "payment.subscription.renew"} {
		v, _ := chain.Route(context.Background(), heart.Event{Kind: kind})
		if v.Action != WakeUser {
			t.Fatalf("kind %q action = %v, want WakeUser", kind, v.Action)
		}
	}
	// A non-high-risk kind falls through to the Cognize default.
	v, _ := chain.Route(context.Background(), heart.Event{Kind: "mail.received"})
	if v.Action != Cognize {
		t.Fatalf("non-high-risk action = %v, want Cognize", v.Action)
	}
}

type labeledEvent struct {
	Source   string `json:"source"`
	Kind     string `json:"kind"`
	Payload  string `json:"payload"`
	Expected string `json:"expected"`
}

// TestLabeledEventSet enforces the routing acceptance criteria over a labeled
// corpus: WakeUser recall must be 100% (missing a high-risk event is intolerable)
// and Ignore precision must exceed 80% (the router must not silently drop events
// that mattered).
func TestLabeledEventSet(t *testing.T) {
	data, err := os.ReadFile("testdata/labeled_events.json")
	if err != nil {
		t.Fatalf("read labeled events: %v", err)
	}
	var events []labeledEvent
	if err := json.Unmarshal(data, &events); err != nil {
		t.Fatalf("decode labeled events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("labeled event set is empty")
	}

	// The router under test: the same tiers production uses, minus the model tier
	// (no live model in unit tests). Rules cover the ignorable families.
	rules := NewRulesRouter([]Rule{
		{Kind: "fs.modified", Contains: ".tmp", Action: "ignore"},
		{Kind: "spam.promo", Action: "ignore"},
		{Source: "newsletter", Action: "ignore"},
	})
	chain := NewChain(rules, nil)
	chain.SetHighRiskKinds(DefaultHighRiskKinds())

	ctx := context.Background()
	var wantWake, gotWake int
	var ignoreVerdicts, ignoreCorrect int

	for _, ev := range events {
		v, err := chain.Route(ctx, heart.Event{Source: ev.Source, Kind: ev.Kind, Payload: ev.Payload})
		if err != nil {
			t.Fatalf("Route(%s/%s) error = %v", ev.Source, ev.Kind, err)
		}
		if ev.Expected == "wake_user" {
			wantWake++
			if v.Action == WakeUser {
				gotWake++
			} else {
				t.Errorf("MISS: %s/%s expected wake_user, got %v", ev.Source, ev.Kind, v.Action)
			}
		}
		if v.Action == Ignore {
			ignoreVerdicts++
			if ev.Expected == "ignore" {
				ignoreCorrect++
			}
		}
	}

	// WakeUser recall: zero tolerance for misses.
	if wantWake == 0 {
		t.Fatal("labeled set has no wake_user events")
	}
	if gotWake != wantWake {
		t.Fatalf("WakeUser recall = %d/%d, want 100%%", gotWake, wantWake)
	}

	// Ignore precision > 80%. Require at least one Ignore verdict so the metric is
	// not vacuously satisfied by a router that never ignores anything.
	if ignoreVerdicts == 0 {
		t.Fatal("router produced no Ignore verdicts; precision check would be vacuous")
	}
	precision := float64(ignoreCorrect) / float64(ignoreVerdicts)
	if precision <= 0.80 {
		t.Fatalf("Ignore precision = %.2f (%d/%d), want > 0.80", precision, ignoreCorrect, ignoreVerdicts)
	}
}
