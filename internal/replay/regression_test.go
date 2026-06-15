package replay

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Forest-Isle/daimon/internal/agent"
)

// scorableSession builds a session with one pure-text exchange (so Rescore will
// actually score it) under the given id.
func scorableSession(t *testing.T, id string) Session {
	t.Helper()
	msgs := []agent.CompletionMessage{{Role: "user", Content: "q for " + id}}
	return Session{
		SessionID: id,
		Exchanges: []agent.ProviderExchange{
			{SessionID: id, ResponseText: "baseline " + id, MessagesJSON: msgsJSON(t, msgs)},
		},
	}
}

func ids(sessions []Session) []string {
	out := make([]string, len(sessions))
	for i, s := range sessions {
		out[i] = s.SessionID
	}
	return out
}

func TestRecentTakesTail(t *testing.T) {
	sessions := []Session{{SessionID: "a"}, {SessionID: "b"}, {SessionID: "c"}, {SessionID: "d"}}

	if got := ids(Recent(sessions, 2)); len(got) != 2 || got[0] != "c" || got[1] != "d" {
		t.Fatalf("Recent(2) = %v, want [c d]", got)
	}
	// n>=len and n<=0 return everything.
	if got := Recent(sessions, 10); len(got) != 4 {
		t.Fatalf("Recent(10) = %v, want all 4", ids(got))
	}
	if got := Recent(sessions, 0); len(got) != 4 {
		t.Fatalf("Recent(0) = %v, want all 4", ids(got))
	}
	if got := Recent(nil, 3); got != nil {
		t.Fatalf("Recent(nil) = %v, want nil", got)
	}
}

func TestSelectRegressionSalvagedOnly(t *testing.T) {
	sessions := []Session{
		{SessionID: "a"},
		{SessionID: "b", Salvaged: true},
		{SessionID: "c"},
		{SessionID: "d", Salvaged: true},
	}
	got := ids(SelectRegression(sessions, RegressionCriteria{IncludeSalvaged: true}))
	if len(got) != 2 || got[0] != "b" || got[1] != "d" {
		t.Fatalf("salvaged set = %v, want [b d] in order", got)
	}
}

func TestSelectRegressionCorrectionOnly(t *testing.T) {
	sessions := []Session{{SessionID: "a"}, {SessionID: "b", Salvaged: true}, {SessionID: "c"}}
	// IncludeSalvaged false: only correction ids enroll, salvaged ignored.
	crit := RegressionCriteria{CorrectionSessionIDs: map[string]bool{"a": true, "c": true}}
	got := ids(SelectRegression(sessions, crit))
	if len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Fatalf("correction set = %v, want [a c]", got)
	}
}

func TestSelectRegressionUnionAndDedup(t *testing.T) {
	sessions := []Session{
		{SessionID: "a", Salvaged: true},
		{SessionID: "b"},
		{SessionID: "a", Salvaged: true}, // duplicate id must not enroll twice
		{SessionID: "c"},
	}
	crit := RegressionCriteria{
		IncludeSalvaged:      true,
		CorrectionSessionIDs: map[string]bool{"a": true, "b": true}, // a overlaps salvaged
	}
	got := ids(SelectRegression(sessions, crit))
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("union/dedup = %v, want [a b] once each", got)
	}
}

func TestSelectRegressionSkipsEmptyID(t *testing.T) {
	sessions := []Session{{SessionID: "", Salvaged: true}, {SessionID: "a", Salvaged: true}}
	got := ids(SelectRegression(sessions, RegressionCriteria{IncludeSalvaged: true}))
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("empty-id session must not enroll: %v", got)
	}
}

func TestSelectRegressionEmptyWhenNothingMatches(t *testing.T) {
	sessions := []Session{{SessionID: "a"}, {SessionID: "b"}}
	// IncludeSalvaged false and no correction ids → nothing enrolls.
	if got := SelectRegression(sessions, RegressionCriteria{}); len(got) != 0 {
		t.Fatalf("want empty set, got %v", ids(got))
	}
}

func TestCanaryPassesWhenNoRegressions(t *testing.T) {
	sessions := []Session{scorableSession(t, "a"), scorableSession(t, "b")}
	cand := &stubCandidate{text: "candidate"}
	judge := &stubJudge{reply: `{"score":85,"regression":false}`}

	cr, err := Canary(context.Background(), sessions, cand, judge, CanaryOptions{}, fixedClock())
	if err != nil {
		t.Fatalf("Canary: %v", err)
	}
	if !cr.Passed {
		t.Fatalf("expected pass, got %+v", cr)
	}
	if cr.Compared != 2 || cr.Sessions != 2 {
		t.Fatalf("compared=%d sessions=%d, want 2/2", cr.Compared, cr.Sessions)
	}
}

func TestCanaryFailsOnRegression(t *testing.T) {
	sessions := []Session{scorableSession(t, "a")}
	cand := &stubCandidate{text: "worse"}
	judge := &stubJudge{reply: `{"score":10,"regression":true}`}

	cr, err := Canary(context.Background(), sessions, cand, judge, CanaryOptions{}, fixedClock())
	if err != nil {
		t.Fatalf("Canary: %v", err)
	}
	if cr.Passed {
		t.Fatal("a regression must fail the canary")
	}
	if cr.Regressions != 1 {
		t.Fatalf("regressions=%d, want 1", cr.Regressions)
	}
}

func TestCanaryFailsOnEmptyEvidence(t *testing.T) {
	// No sessions at all → nothing compared → cannot certify → fail closed.
	cand := &stubCandidate{text: "x"}
	judge := &stubJudge{reply: `{"score":99}`}
	cr, err := Canary(context.Background(), nil, cand, judge, CanaryOptions{}, fixedClock())
	if err != nil {
		t.Fatalf("Canary: %v", err)
	}
	if cr.Passed || cr.Compared != 0 {
		t.Fatalf("empty evidence must fail closed: %+v", cr)
	}
}

func TestCanaryFailsWhenAllSkipped(t *testing.T) {
	// A session with only a pure tool-call (empty baseline) turn: scorable=0, so the
	// run compares nothing and must not pass even with zero regressions.
	msgs := []agent.CompletionMessage{{Role: "user", Content: "q"}}
	sessions := []Session{{SessionID: "a", Exchanges: []agent.ProviderExchange{
		{SessionID: "a", ResponseText: "", MessagesJSON: msgsJSON(t, msgs)},
	}}}
	cand := &stubCandidate{text: "x"}
	judge := &stubJudge{reply: `{"score":99}`}

	cr, err := Canary(context.Background(), sessions, cand, judge, CanaryOptions{}, fixedClock())
	if err != nil {
		t.Fatalf("Canary: %v", err)
	}
	if cr.Passed {
		t.Fatalf("all-skipped run is no evidence, must fail: %+v", cr)
	}
}

func TestCanaryFailsWhenCapped(t *testing.T) {
	// Two scorable exchanges but cap=1: coverage incomplete → fail closed even
	// though the one scored exchange had no regression.
	msgs := []agent.CompletionMessage{{Role: "user", Content: "q"}}
	sessions := []Session{{SessionID: "a", Exchanges: []agent.ProviderExchange{
		{SessionID: "a", Iteration: 0, ResponseText: "b0", MessagesJSON: msgsJSON(t, msgs)},
		{SessionID: "a", Iteration: 1, ResponseText: "b1", MessagesJSON: msgsJSON(t, msgs)},
	}}}
	cand := &stubCandidate{text: "c"}
	judge := &stubJudge{reply: `{"score":80,"regression":false}`}

	cr, err := Canary(context.Background(), sessions, cand, judge, CanaryOptions{Rescore: RescoreOptions{MaxExchanges: 1}}, fixedClock())
	if err != nil {
		t.Fatalf("Canary: %v", err)
	}
	if !cr.Capped {
		t.Fatal("expected Capped run")
	}
	if cr.Passed {
		t.Fatal("a capped (incomplete-coverage) run must not pass")
	}
}

func TestCanaryErrorTolerance(t *testing.T) {
	// One scorable exchange whose candidate call errors → Errors=1, Compared=0.
	sessions := []Session{scorableSession(t, "a")}
	cand := &stubCandidate{err: context.Canceled}
	judge := &stubJudge{reply: `{"score":50}`}

	// Default MaxErrors=0: one error fails (and Compared=0 also fails).
	cr, err := Canary(context.Background(), sessions, cand, judge, CanaryOptions{}, fixedClock())
	if err != nil {
		t.Fatalf("Canary: %v", err)
	}
	if cr.Passed {
		t.Fatalf("an unscored exchange must fail at MaxErrors=0: %+v", cr)
	}
	if cr.Errors != 1 {
		t.Fatalf("errors=%d, want 1", cr.Errors)
	}
}

func TestCanaryPropagatesDepError(t *testing.T) {
	// Rescore rejects nil deps; Canary must surface that error, not a false verdict.
	if _, err := Canary(context.Background(), nil, nil, &stubJudge{}, CanaryOptions{}, fixedClock()); err == nil {
		t.Fatal("nil candidate must error through Canary")
	}
}

func TestCanaryFailsOnUnverifiedActionTurn(t *testing.T) {
	// A session with one action turn (baseline made tool calls → skipped, unverified)
	// plus one clean text turn. The clean text turn alone must NOT certify the change:
	// its tool behavior went unchecked, so the gate fails closed by default.
	msgs := []agent.CompletionMessage{{Role: "user", Content: "do it"}}
	toolCalls, _ := json.Marshal([]agent.ToolUseBlock{{ID: "t1", Name: "bash", Input: "{}"}})
	sessions := []Session{{SessionID: "a", Exchanges: []agent.ProviderExchange{
		{SessionID: "a", Iteration: 0, ResponseText: "running it", MessagesJSON: msgsJSON(t, msgs), ToolCallsJSON: toolCalls},
		{SessionID: "a", Iteration: 1, ResponseText: "answer", MessagesJSON: msgsJSON(t, msgs)},
	}}}
	cand := &stubCandidate{text: "answer2"}
	judge := &stubJudge{reply: `{"score":90,"regression":false}`}

	cr, err := Canary(context.Background(), sessions, cand, judge, CanaryOptions{}, fixedClock())
	if err != nil {
		t.Fatalf("Canary: %v", err)
	}
	if cr.SkippedAction != 1 {
		t.Fatalf("SkippedAction=%d, want 1", cr.SkippedAction)
	}
	if cr.Passed {
		t.Fatal("an unverified action turn must fail the canary by default")
	}

	// A text-only change may opt in to accept text-only certification.
	cr, err = Canary(context.Background(), sessions, cand, judge, CanaryOptions{AllowSkippedActions: true}, fixedClock())
	if err != nil {
		t.Fatalf("Canary: %v", err)
	}
	if !cr.Passed {
		t.Fatalf("AllowSkippedActions must let a clean text comparison pass: %+v", cr)
	}
}

func TestCanaryCountsEmptyResponseActionTurn(t *testing.T) {
	// An action turn with NO prose (model called a tool and said nothing) hits the
	// empty-baseline skip — but it is still an unverified action turn and must count
	// toward SkippedAction so the clean text turn alone cannot certify the change.
	msgs := []agent.CompletionMessage{{Role: "user", Content: "do it"}}
	toolCalls, _ := json.Marshal([]agent.ToolUseBlock{{ID: "t1", Name: "bash", Input: "{}"}})
	sessions := []Session{{SessionID: "a", Exchanges: []agent.ProviderExchange{
		{SessionID: "a", Iteration: 0, ResponseText: "", MessagesJSON: msgsJSON(t, msgs), ToolCallsJSON: toolCalls},
		{SessionID: "a", Iteration: 1, ResponseText: "answer", MessagesJSON: msgsJSON(t, msgs)},
	}}}
	cand := &stubCandidate{text: "answer2"}
	judge := &stubJudge{reply: `{"score":90,"regression":false}`}

	cr, err := Canary(context.Background(), sessions, cand, judge, CanaryOptions{}, fixedClock())
	if err != nil {
		t.Fatalf("Canary: %v", err)
	}
	if cr.SkippedAction != 1 {
		t.Fatalf("SkippedAction=%d, want 1 (empty-prose action turn must count)", cr.SkippedAction)
	}
	if cr.Passed {
		t.Fatal("an empty-prose action turn must fail the canary by default")
	}
}

func TestCanaryFailsOnSchemaIncompleteVerdict(t *testing.T) {
	// Judge returns a score but omits the required regression field: schema-incomplete,
	// treated as indeterminate (not a silent non-regression), must not pass.
	sessions := []Session{scorableSession(t, "a")}
	cand := &stubCandidate{text: "candidate"}
	judge := &stubJudge{reply: `{"score":80}`}

	cr, err := Canary(context.Background(), sessions, cand, judge, CanaryOptions{}, fixedClock())
	if err != nil {
		t.Fatalf("Canary: %v", err)
	}
	if cr.Indeterminate != 1 {
		t.Fatalf("Indeterminate=%d, want 1", cr.Indeterminate)
	}
	if cr.Passed {
		t.Fatal("a verdict missing the regression field must fail the canary")
	}
}

func TestCanaryFailsOnIndeterminateJudge(t *testing.T) {
	// The judge reply is unparseable: Rescore degrades it to a neutral, non-regression
	// score for diagnostics, but the canary must not read that as a clean pass.
	sessions := []Session{scorableSession(t, "a")}
	cand := &stubCandidate{text: "candidate"}
	judge := &stubJudge{reply: "I cannot produce JSON."}

	cr, err := Canary(context.Background(), sessions, cand, judge, CanaryOptions{}, fixedClock())
	if err != nil {
		t.Fatalf("Canary: %v", err)
	}
	if cr.Compared != 1 || cr.Indeterminate != 1 {
		t.Fatalf("want compared=1 indeterminate=1, got %+v", cr)
	}
	if cr.Passed {
		t.Fatal("an indeterminate judge verdict must fail the canary")
	}
}
