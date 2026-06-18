package replay

import "sort"

// TrajectoryResult is one session's multi-step adherence: how deep the candidate
// stayed faithful to the recorded trajectory before the first divergence. After
// that point the recorded context no longer reflects the state the candidate
// would actually be in, so downstream exchanges are contaminated and not counted
// as adherence (blueprint §4.10 inc2: honest prefix-adherence, not full
// multi-step equivalence — divergence past a side-effecting step cannot be
// faithfully replayed offline).
type TrajectoryResult struct {
	SessionID           string
	Total               int // scored exchanges in this session (entries in Results)
	AdherenceDepth      int // leading exchanges before the first break
	DivergedAtIteration int // Iteration of the first break, or -1 if none
	FullyAdhered        bool
}

// TrajectorySummary aggregates TrajectoryResults across a run.
type TrajectorySummary struct {
	Sessions         int
	FullyAdhered     int
	Diverged         int
	TotalExchanges   int
	AdheredExchanges int
	AdherenceRatio   float64 // AdheredExchanges / TotalExchanges, 0 when no exchanges
}

// trajectoryBreaks reports whether an exchange ends the faithful prefix. A
// regression is a clear divergence; an indeterminate verdict or an errored
// exchange is a point we could not certify, so we fail closed and stop claiming
// adherence there too (we cannot assert the candidate stayed on-rails through a
// step we could not judge).
func trajectoryBreaks(r ExchangeResult) bool {
	return r.Verdict.Regression || r.Verdict.Indeterminate || r.Err != ""
}

// Trajectories groups re-scored exchanges by session (first-appearance order),
// orders each session by Iteration, and computes the adherence depth: the number
// of leading exchanges before the first break. Pure: no side effects.
func Trajectories(results []ExchangeResult) []TrajectoryResult {
	order := []string{}
	groups := map[string][]ExchangeResult{}
	for _, r := range results {
		if _, ok := groups[r.SessionID]; !ok {
			order = append(order, r.SessionID)
		}
		groups[r.SessionID] = append(groups[r.SessionID], r)
	}
	out := make([]TrajectoryResult, 0, len(order))
	for _, sid := range order {
		ex := groups[sid]
		sort.SliceStable(ex, func(i, j int) bool { return ex[i].Iteration < ex[j].Iteration })
		depth := 0
		divergedAt := -1
		for _, r := range ex {
			if trajectoryBreaks(r) {
				divergedAt = r.Iteration
				break
			}
			depth++
		}
		out = append(out, TrajectoryResult{
			SessionID:           sid,
			Total:               len(ex),
			AdherenceDepth:      depth,
			DivergedAtIteration: divergedAt,
			FullyAdhered:        divergedAt == -1,
		})
	}
	return out
}

// SummarizeTrajectories reduces per-session trajectories to a run-level summary.
func SummarizeTrajectories(trajectories []TrajectoryResult) TrajectorySummary {
	var s TrajectorySummary
	s.Sessions = len(trajectories)
	for _, t := range trajectories {
		if t.FullyAdhered {
			s.FullyAdhered++
		} else {
			s.Diverged++
		}
		s.TotalExchanges += t.Total
		s.AdheredExchanges += t.AdherenceDepth
	}
	if s.TotalExchanges > 0 {
		s.AdherenceRatio = float64(s.AdheredExchanges) / float64(s.TotalExchanges)
	}
	return s
}
