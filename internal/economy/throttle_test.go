package economy

import "testing"

func TestThrottlePolicyConfigured(t *testing.T) {
	if (ThrottlePolicy{}).Configured() {
		t.Fatal("empty policy must not be configured")
	}
	if !(ThrottlePolicy{PerClassBudgetUSD: 1}).Configured() {
		t.Fatal("budget set ⇒ configured")
	}
	if !(ThrottlePolicy{MinCleanRate: 0.5}).Configured() {
		t.Fatal("clean-rate set ⇒ configured")
	}
	// MinEpisodes alone is only a guard for the clean-rate check, not a trigger.
	if (ThrottlePolicy{MinEpisodes: 5}).Configured() {
		t.Fatal("min-episodes alone must not count as configured")
	}
}

func TestClassValueCleanRate(t *testing.T) {
	if got := (ClassValue{Episodes: 4, Clean: 1}).CleanRate(); got != 0.25 {
		t.Fatalf("clean rate = %v, want 0.25", got)
	}
	if got := (ClassValue{Episodes: 0, Clean: 0}).CleanRate(); got != 0 {
		t.Fatalf("zero-episode clean rate = %v, want 0", got)
	}
}

func TestThrottleEvaluateOverBudget(t *testing.T) {
	p := ThrottlePolicy{PerClassBudgetUSD: 10}
	classes := []ClassValue{
		{Class: "chat", Episodes: 5, Clean: 5, USD: 25, Priced: true},    // over
		{Class: "poll", Episodes: 5, Clean: 5, USD: 5, Priced: true},     // under
		{Class: "heavy", Episodes: 5, Clean: 5, USD: 100, Priced: false}, // over but UNPRICED ⇒ not flagged
		{Class: "edge", Episodes: 1, Clean: 1, USD: 10, Priced: true},    // exactly at budget ⇒ not over
	}
	advice := p.Evaluate(classes)
	if len(advice) != 1 {
		t.Fatalf("want 1 flagged (chat), got %d: %+v", len(advice), advice)
	}
	a := advice[0]
	if a.Class != "chat" || !a.OverBudget || a.LowValue {
		t.Fatalf("advice = %+v, want chat over-budget only", a)
	}
	if a.USD != 25 || a.Episodes != 5 {
		t.Fatalf("advice carries wrong figures: %+v", a)
	}
}

func TestThrottleEvaluateLowValue(t *testing.T) {
	p := ThrottlePolicy{MinCleanRate: 0.5, MinEpisodes: 3}
	classes := []ClassValue{
		{Class: "flaky", Episodes: 10, Clean: 2, USD: 1, Priced: true},     // 20% < 50% ⇒ low value
		{Class: "good", Episodes: 10, Clean: 8, USD: 1, Priced: true},      // 80% ⇒ ok
		{Class: "tiny", Episodes: 2, Clean: 0, USD: 1, Priced: true},       // 0% but < MinEpisodes ⇒ NOT flagged
		{Class: "unpriced", Episodes: 10, Clean: 1, USD: 0, Priced: false}, // 10% ⇒ low value, but cost incomplete
	}
	advice := p.Evaluate(classes)
	if len(advice) != 2 {
		t.Fatalf("want 2 flagged (flaky, unpriced), got %d: %+v", len(advice), advice)
	}
	a := advice[0]
	if a.Class != "flaky" || a.OverBudget || !a.LowValue || !a.Priced {
		t.Fatalf("advice = %+v, want flaky low-value priced", a)
	}
	if a.CleanRate != 0.2 {
		t.Fatalf("clean rate = %v, want 0.2", a.CleanRate)
	}
	// A class can be flagged low-value while unpriced; Priced is carried through so
	// the renderer shows "—" for its incomplete cost rather than a misleading $0.
	if u := advice[1]; u.Class != "unpriced" || !u.LowValue || u.Priced {
		t.Fatalf("advice = %+v, want unpriced low-value with Priced=false", u)
	}
}

func TestThrottleEvaluateBothAndOrder(t *testing.T) {
	p := ThrottlePolicy{PerClassBudgetUSD: 10, MinCleanRate: 0.5, MinEpisodes: 3}
	classes := []ClassValue{
		{Class: "b", Episodes: 5, Clean: 5, USD: 50, Priced: true}, // over budget, fine value
		{Class: "a", Episodes: 5, Clean: 1, USD: 50, Priced: true}, // over budget AND low value
		{Class: "c", Episodes: 5, Clean: 5, USD: 1, Priced: true},  // neither ⇒ omitted
	}
	advice := p.Evaluate(classes)
	if len(advice) != 2 {
		t.Fatalf("want 2 flagged, got %d: %+v", len(advice), advice)
	}
	// Input order preserved: b before a.
	if advice[0].Class != "b" || advice[1].Class != "a" {
		t.Fatalf("order = %q,%q, want b,a (input order)", advice[0].Class, advice[1].Class)
	}
	if !advice[0].OverBudget || advice[0].LowValue {
		t.Fatalf("b = %+v, want over-budget only", advice[0])
	}
	if !advice[1].OverBudget || !advice[1].LowValue {
		t.Fatalf("a = %+v, want over-budget AND low-value", advice[1])
	}
}

func TestThrottleEvaluateUnconfiguredFlagsNothing(t *testing.T) {
	// An unset policy (all thresholds zero) must flag nothing even on bad classes.
	p := ThrottlePolicy{}
	classes := []ClassValue{
		{Class: "x", Episodes: 100, Clean: 0, USD: 9999, Priced: true},
	}
	if advice := p.Evaluate(classes); len(advice) != 0 {
		t.Fatalf("unconfigured policy must flag nothing, got %+v", advice)
	}
}

func TestThrottleEvaluateMinEpisodesZeroNoGuard(t *testing.T) {
	// MinEpisodes 0 ⇒ no minimum: even a single bad episode is flagged.
	p := ThrottlePolicy{MinCleanRate: 0.5} // MinEpisodes unset
	classes := []ClassValue{
		{Class: "single", Episodes: 1, Clean: 0, USD: 1, Priced: true},
	}
	advice := p.Evaluate(classes)
	if len(advice) != 1 || !advice[0].LowValue {
		t.Fatalf("want single flagged low-value with no episode minimum, got %+v", advice)
	}
}
