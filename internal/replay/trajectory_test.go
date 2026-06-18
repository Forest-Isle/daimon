package replay

import "testing"

func okExchange(session string, iteration int) ExchangeResult {
	return ExchangeResult{SessionID: session, Iteration: iteration}
}

func TestTrajectoriesFullyAdheredSession(t *testing.T) {
	got := Trajectories([]ExchangeResult{
		okExchange("s1", 0),
		okExchange("s1", 1),
		okExchange("s1", 2),
	})

	if len(got) != 1 {
		t.Fatalf("Trajectories length = %d, want 1", len(got))
	}
	if got[0].Total != 3 || got[0].AdherenceDepth != 3 || !got[0].FullyAdhered || got[0].DivergedAtIteration != -1 {
		t.Fatalf("trajectory = %+v, want full adherence over 3 exchanges", got[0])
	}
}

func TestTrajectoriesRegressionBreakStopsPrefix(t *testing.T) {
	got := Trajectories([]ExchangeResult{
		okExchange("s1", 0),
		okExchange("s1", 1),
		{SessionID: "s1", Iteration: 2, Verdict: Verdict{Regression: true}},
		okExchange("s1", 3),
		okExchange("s1", 4),
	})

	if len(got) != 1 {
		t.Fatalf("Trajectories length = %d, want 1", len(got))
	}
	if got[0].AdherenceDepth != 2 || got[0].DivergedAtIteration != 2 || got[0].FullyAdhered {
		t.Fatalf("trajectory = %+v, want depth=2 diverged_at=2 not full", got[0])
	}
}

func TestTrajectoriesIndeterminateBreak(t *testing.T) {
	got := Trajectories([]ExchangeResult{
		{SessionID: "s1", Iteration: 0, Verdict: Verdict{Indeterminate: true}},
		okExchange("s1", 1),
	})

	if len(got) != 1 {
		t.Fatalf("Trajectories length = %d, want 1", len(got))
	}
	if got[0].AdherenceDepth != 0 || got[0].DivergedAtIteration != 0 || got[0].FullyAdhered {
		t.Fatalf("trajectory = %+v, want indeterminate at first exchange to break", got[0])
	}
}

func TestTrajectoriesErrBreak(t *testing.T) {
	got := Trajectories([]ExchangeResult{
		okExchange("s1", 0),
		{SessionID: "s1", Iteration: 1, Err: "candidate: unavailable"},
		okExchange("s1", 2),
	})

	if len(got) != 1 {
		t.Fatalf("Trajectories length = %d, want 1", len(got))
	}
	if got[0].AdherenceDepth != 1 || got[0].DivergedAtIteration != 1 || got[0].FullyAdhered {
		t.Fatalf("trajectory = %+v, want error at iteration 1 to break", got[0])
	}
}

func TestTrajectoriesSortsByIterationBeforeDepth(t *testing.T) {
	got := Trajectories([]ExchangeResult{
		okExchange("s1", 2),
		okExchange("s1", 0),
		{SessionID: "s1", Iteration: 1, Verdict: Verdict{Regression: true}},
		okExchange("s1", 3),
	})

	if len(got) != 1 {
		t.Fatalf("Trajectories length = %d, want 1", len(got))
	}
	if got[0].AdherenceDepth != 1 || got[0].DivergedAtIteration != 1 {
		t.Fatalf("trajectory = %+v, want sorted depth=1 diverged_at=1", got[0])
	}
}

func TestTrajectoriesMixedSessionsAndSummary(t *testing.T) {
	trajectories := Trajectories([]ExchangeResult{
		okExchange("full", 0),
		okExchange("diverged", 0),
		okExchange("full", 1),
		{SessionID: "diverged", Iteration: 1, Verdict: Verdict{Regression: true}},
		okExchange("diverged", 2),
	})

	if len(trajectories) != 2 {
		t.Fatalf("Trajectories length = %d, want 2", len(trajectories))
	}
	if trajectories[0].SessionID != "full" || trajectories[0].Total != 2 ||
		trajectories[0].AdherenceDepth != 2 || !trajectories[0].FullyAdhered ||
		trajectories[0].DivergedAtIteration != -1 {
		t.Fatalf("first trajectory = %+v, want fully adhered full session", trajectories[0])
	}
	if trajectories[1].SessionID != "diverged" || trajectories[1].Total != 3 ||
		trajectories[1].AdherenceDepth != 1 || trajectories[1].FullyAdhered ||
		trajectories[1].DivergedAtIteration != 1 {
		t.Fatalf("second trajectory = %+v, want diverged session depth 1", trajectories[1])
	}

	summary := SummarizeTrajectories(trajectories)
	if summary.Sessions != 2 || summary.FullyAdhered != 1 || summary.Diverged != 1 ||
		summary.TotalExchanges != 5 || summary.AdheredExchanges != 3 ||
		summary.AdherenceRatio != 0.6 {
		t.Fatalf("summary = %+v, want 2 sessions, 1 full, 1 diverged, 3/5 adherence", summary)
	}
}

func TestTrajectoriesEmpty(t *testing.T) {
	trajectories := Trajectories(nil)
	if len(trajectories) != 0 {
		t.Fatalf("Trajectories(nil) length = %d, want 0", len(trajectories))
	}
	summary := SummarizeTrajectories(trajectories)
	if summary != (TrajectorySummary{}) {
		t.Fatalf("summary = %+v, want zero value", summary)
	}
	if summary.AdherenceRatio != 0 {
		t.Fatalf("AdherenceRatio = %v, want 0", summary.AdherenceRatio)
	}
}
