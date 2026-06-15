package economy

// Throttle advisor (DAIMON_BLUEPRINT.md §4.11): given each activity class's cost
// and value (its clean-outcome rate), flag the classes that spend more than their
// budget or deliver poor value. This phase is OBSERVE-ONLY — Evaluate returns
// advice that the report surfaces; nothing acts on it. Auto-enforcement
// (down-routing a flagged class, or reducing its cadence) is a separate, gated step
// that consumes this same advice.

// ClassValue is one activity class's cost-vs-value summary, the advisor's input.
// Clean is how many of Episodes closed cleanly (fully verified). USD is the class's
// dollar cost; Priced is false when any of its models is unpriced, so the cost — and
// any budget comparison — is incomplete and must not trigger a budget flag.
type ClassValue struct {
	Class    string
	Episodes int
	Clean    int
	USD      float64
	Priced   bool
}

// CleanRate is the fraction of the class's episodes that closed cleanly, in [0,1]
// (0 when the class had no episodes).
func (c ClassValue) CleanRate() float64 {
	if c.Episodes <= 0 {
		return 0
	}
	return float64(c.Clean) / float64(c.Episodes)
}

// ThrottlePolicy holds the soft thresholds the advisor flags against. A zero
// threshold disables that check (so an unset policy flags nothing).
type ThrottlePolicy struct {
	PerClassBudgetUSD float64
	MinCleanRate      float64
	MinEpisodes       int
}

// Configured reports whether any threshold is active, so a caller can skip the
// recommendations section entirely when the operator has not set a policy.
func (p ThrottlePolicy) Configured() bool {
	return p.PerClassBudgetUSD > 0 || p.MinCleanRate > 0
}

// ThrottleAdvice is a flagged class plus why it was flagged. It is advisory only —
// nothing acts on it in this phase. Priced mirrors the class's cost completeness so
// a renderer can show "—" for an incomplete cost (a class can be flagged LowValue
// while unpriced; OverBudget always implies Priced).
type ThrottleAdvice struct {
	Class      string
	OverBudget bool
	LowValue   bool
	USD        float64
	Priced     bool
	CleanRate  float64
	Episodes   int
}

// Evaluate flags each class that exceeds the budget or delivers poor value, in the
// input order. A class is flagged:
//   - OverBudget only when it is priced (an incomplete cost must not trigger a
//     budget action) and its cost exceeds a positive PerClassBudgetUSD;
//   - LowValue only when MinCleanRate is set, the class has at least MinEpisodes
//     episodes (so a tiny sample is not punished), and its clean rate is below the
//     threshold.
//
// Classes that trip neither are omitted. Evaluate is pure: it reads nothing and
// changes nothing.
func (p ThrottlePolicy) Evaluate(classes []ClassValue) []ThrottleAdvice {
	var out []ThrottleAdvice
	for _, c := range classes {
		overBudget := p.PerClassBudgetUSD > 0 && c.Priced && c.USD > p.PerClassBudgetUSD
		lowValue := p.MinCleanRate > 0 && c.Episodes >= p.MinEpisodes && c.CleanRate() < p.MinCleanRate
		if !overBudget && !lowValue {
			continue
		}
		out = append(out, ThrottleAdvice{
			Class:      c.Class,
			OverBudget: overBudget,
			LowValue:   lowValue,
			USD:        c.USD,
			Priced:     c.Priced,
			CleanRate:  c.CleanRate(),
			Episodes:   c.Episodes,
		})
	}
	return out
}
