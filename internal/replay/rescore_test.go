package replay

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/mind"
)

// stubCandidate returns a fixed response (or error) and records the requests it
// received, so the harness's request reconstruction can be asserted.
type stubCandidate struct {
	text      string
	toolCalls []mind.ToolUseBlock
	err       error
	usage     mind.Usage
	requests  []mind.CompletionRequest
}

func (s *stubCandidate) Complete(_ context.Context, req mind.CompletionRequest) (*mind.CompletionResponse, error) {
	s.requests = append(s.requests, req)
	if s.err != nil {
		return nil, s.err
	}
	return &mind.CompletionResponse{Text: s.text, ToolCalls: s.toolCalls, StopReason: mind.StopEndTurn, Usage: s.usage}, nil
}

// stubJudge returns a fixed reply (or error) and records the user message it saw.
type stubJudge struct {
	reply    string
	err      error
	gotInput string
}

func (s *stubJudge) Complete(_ context.Context, _, userMessage string) (string, error) {
	s.gotInput = userMessage
	if s.err != nil {
		return "", s.err
	}
	return s.reply, nil
}

func msgsJSON(t *testing.T, msgs []mind.CompletionMessage) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(msgs)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sessionWith(exchanges ...agent.ProviderExchange) Session {
	return Session{SessionID: "s1", Exchanges: exchanges}
}

func TestRescoreScoresAndAggregates(t *testing.T) {
	msgs := []mind.CompletionMessage{
		{Role: "user", Content: "what is the capital of France?"},
	}
	sessions := []Session{sessionWith(
		agent.ProviderExchange{SessionID: "s1", Iteration: 0, ResponseText: "Paris.", MessagesJSON: msgsJSON(t, msgs), DurationMs: 120},
		agent.ProviderExchange{SessionID: "s1", Iteration: 1, ResponseText: "", MessagesJSON: msgsJSON(t, msgs), DurationMs: 5}, // pure tool-call: skipped
	)}
	cand := &stubCandidate{text: "The capital of France is Paris."}
	judge := &stubJudge{reply: `{"score":90,"regression":false,"note":"more complete"}`}

	rep, err := Rescore(context.Background(), sessions, cand, judge, RescoreOptions{Model: "candidate-model"}, fixedClock())
	if err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	if rep.Compared != 1 || rep.Skipped != 1 || rep.Errors != 0 {
		t.Fatalf("counts = %+v, want compared=1 skipped=1 errors=0", rep)
	}
	if rep.Regressions != 0 || rep.AvgScore != 90 {
		t.Fatalf("regressions=%d avg=%d, want 0 / 90", rep.Regressions, rep.AvgScore)
	}
	// The reconstructed request must carry the candidate model + the recorded turn.
	if len(cand.requests) != 1 || cand.requests[0].Model != "candidate-model" {
		t.Fatalf("candidate request = %+v", cand.requests)
	}
	if cand.requests[0].Messages[0].Content != "what is the capital of France?" {
		t.Fatalf("messages not reconstructed: %+v", cand.requests[0].Messages)
	}
	// The judge must have seen the last user turn as context.
	if want := "what is the capital of France?"; !strings.Contains(judge.gotInput, want) {
		t.Fatalf("judge context missing %q:\n%s", want, judge.gotInput)
	}
}

func TestRescoreCountsRegression(t *testing.T) {
	msgs := []mind.CompletionMessage{{Role: "user", Content: "help"}}
	sessions := []Session{sessionWith(
		agent.ProviderExchange{SessionID: "s1", ResponseText: "good baseline", MessagesJSON: msgsJSON(t, msgs)},
	)}
	cand := &stubCandidate{text: "worse"}
	judge := &stubJudge{reply: "thinking... {\"score\":20,\"regression\":true,\"note\":\"dropped detail\"}"}

	rep, err := Rescore(context.Background(), sessions, cand, judge, RescoreOptions{}, fixedClock())
	if err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	if rep.Compared != 1 || rep.Regressions != 1 {
		t.Fatalf("rep = %+v, want compared=1 regressions=1", rep)
	}
}

func TestRescoreCandidateErrorRecorded(t *testing.T) {
	msgs := []mind.CompletionMessage{{Role: "user", Content: "help"}}
	sessions := []Session{sessionWith(
		agent.ProviderExchange{SessionID: "s1", ResponseText: "baseline", MessagesJSON: msgsJSON(t, msgs)},
	)}
	cand := &stubCandidate{err: errors.New("provider down")}
	judge := &stubJudge{reply: `{"score":50}`}

	rep, err := Rescore(context.Background(), sessions, cand, judge, RescoreOptions{}, fixedClock())
	if err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	if rep.Errors != 1 || rep.Compared != 0 {
		t.Fatalf("rep = %+v, want errors=1 compared=0", rep)
	}
	if len(rep.Results) != 1 || rep.Results[0].Err == "" {
		t.Fatalf("error result not recorded: %+v", rep.Results)
	}
}

func TestRescoreUnparseableJudgeIsNeutral(t *testing.T) {
	msgs := []mind.CompletionMessage{{Role: "user", Content: "help"}}
	sessions := []Session{sessionWith(
		agent.ProviderExchange{SessionID: "s1", ResponseText: "baseline", MessagesJSON: msgsJSON(t, msgs)},
	)}
	cand := &stubCandidate{text: "candidate"}
	judge := &stubJudge{reply: "I cannot produce JSON, sorry."}

	rep, err := Rescore(context.Background(), sessions, cand, judge, RescoreOptions{}, fixedClock())
	if err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	// Unparseable judgment degrades to a neutral, non-regression verdict.
	if rep.Compared != 1 || rep.Regressions != 0 || rep.AvgScore != 50 {
		t.Fatalf("rep = %+v, want compared=1 regressions=0 avg=50", rep)
	}
}

func TestRescoreMaxExchangesCap(t *testing.T) {
	msgs := []mind.CompletionMessage{{Role: "user", Content: "help"}}
	var exchanges []agent.ProviderExchange
	for i := 0; i < 5; i++ {
		exchanges = append(exchanges, agent.ProviderExchange{SessionID: "s1", Iteration: i, ResponseText: "baseline", MessagesJSON: msgsJSON(t, msgs)})
	}
	sessions := []Session{sessionWith(exchanges...)}
	cand := &stubCandidate{text: "c"}
	judge := &stubJudge{reply: `{"score":70,"regression":false}`}

	rep, err := Rescore(context.Background(), sessions, cand, judge, RescoreOptions{MaxExchanges: 2}, fixedClock())
	if err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	if rep.Compared != 2 {
		t.Fatalf("cap not honored: compared=%d, want 2", rep.Compared)
	}
	if !rep.Capped {
		t.Fatal("Capped must be true when the cap stopped the run with exchanges remaining")
	}
}

func TestRescoreJudgesActionTurns(t *testing.T) {
	msgs := []mind.CompletionMessage{{Role: "user", Content: "do it"}}
	toolCalls, _ := json.Marshal([]mind.ToolUseBlock{{ID: "t1", Name: "bash", Input: "{}"}})
	sessions := []Session{sessionWith(
		// text + recorded tool call → action turn, judged at the decision layer.
		agent.ProviderExchange{SessionID: "s1", ResponseText: "running it", MessagesJSON: msgsJSON(t, msgs), ToolCallsJSON: toolCalls},
		// pure text → scored.
		agent.ProviderExchange{SessionID: "s1", Iteration: 1, ResponseText: "answer", MessagesJSON: msgsJSON(t, msgs)},
	)}
	cand := &stubCandidate{text: "answer2", toolCalls: []mind.ToolUseBlock{{ID: "t1", Name: "bash", Input: "{}"}}}
	judge := &stubJudge{reply: `{"score":80,"regression":false}`}

	rep, err := Rescore(context.Background(), sessions, cand, judge, RescoreOptions{}, fixedClock())
	if err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	if rep.Compared != 2 || rep.ActionCompared != 1 || rep.Skipped != 0 {
		t.Fatalf("rep = %+v, want compared=2 actionCompared=1 skipped=0", rep)
	}
}

func TestRescoreActionTurnRegression(t *testing.T) {
	msgs := []mind.CompletionMessage{{Role: "user", Content: "do it"}}
	toolCalls, _ := json.Marshal([]mind.ToolUseBlock{{ID: "t1", Name: "bash", Input: `{"cmd":"safe"}`}})
	sessions := []Session{sessionWith(
		agent.ProviderExchange{SessionID: "s1", ResponseText: "running it", MessagesJSON: msgsJSON(t, msgs), ToolCallsJSON: toolCalls},
	)}
	cand := &stubCandidate{text: "done"}
	judge := &stubJudge{reply: `{"score":15,"regression":true,"note":"missed the required tool"}`}

	rep, err := Rescore(context.Background(), sessions, cand, judge, RescoreOptions{}, fixedClock())
	if err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	if rep.Regressions != 1 || rep.ActionCompared != 1 || rep.Indeterminate != 1 {
		t.Fatalf("rep = %+v, want regressions=1 actionCompared=1 indeterminate=1", rep)
	}
}

func TestRescoreUndecodableActionPayloadFailsClosed(t *testing.T) {
	msgs := []mind.CompletionMessage{{Role: "user", Content: "do it"}}
	sessions := []Session{sessionWith(
		agent.ProviderExchange{
			SessionID: "s1", ResponseText: "running it", MessagesJSON: msgsJSON(t, msgs),
			ToolCallsJSON: json.RawMessage(`{"not":"an array"}`),
		},
	)}
	cand := &stubCandidate{text: "answer"}
	judge := &stubJudge{reply: `{"score":90,"regression":false}`}

	rep, err := Rescore(context.Background(), sessions, cand, judge, RescoreOptions{}, fixedClock())
	if err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	if rep.SkippedAction != 1 || rep.Skipped != 1 || rep.Compared != 0 {
		t.Fatalf("rep = %+v, want skippedAction=1 skipped=1 compared=0", rep)
	}
	if len(cand.requests) != 0 {
		t.Fatalf("candidate must not be called for undecodable action payload: %+v", cand.requests)
	}
}

func TestRescoreActionCandidateNoToolCallIndeterminate(t *testing.T) {
	msgs := []mind.CompletionMessage{{Role: "user", Content: "do it"}}
	toolCalls, _ := json.Marshal([]mind.ToolUseBlock{{ID: "t1", Name: "bash", Input: "{}"}})
	sessions := []Session{sessionWith(
		agent.ProviderExchange{SessionID: "s1", ResponseText: "running it", MessagesJSON: msgsJSON(t, msgs), ToolCallsJSON: toolCalls},
	)}
	cand := &stubCandidate{text: "I can answer without a tool."}
	judge := &stubJudge{reply: `{"score":90,"regression":false}`}

	rep, err := Rescore(context.Background(), sessions, cand, judge, RescoreOptions{}, fixedClock())
	if err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	if rep.Indeterminate != 1 || rep.ActionCompared != 1 || rep.Regressions != 0 {
		t.Fatalf("rep = %+v, want indeterminate=1 actionCompared=1 regressions=0", rep)
	}
}

func TestRescoreEmptyBaselineActionTurnJudged(t *testing.T) {
	msgs := []mind.CompletionMessage{{Role: "user", Content: "do it"}}
	toolCalls, _ := json.Marshal([]mind.ToolUseBlock{{ID: "t1", Name: "bash", Input: "{}"}})
	sessions := []Session{sessionWith(
		agent.ProviderExchange{SessionID: "s1", ResponseText: "", MessagesJSON: msgsJSON(t, msgs), ToolCallsJSON: toolCalls},
	)}
	cand := &stubCandidate{toolCalls: []mind.ToolUseBlock{{ID: "t1", Name: "bash", Input: "{}"}}}
	judge := &stubJudge{reply: `{"score":90,"regression":false}`}

	rep, err := Rescore(context.Background(), sessions, cand, judge, RescoreOptions{}, fixedClock())
	if err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	if rep.ActionCompared != 1 || rep.Skipped != 0 || rep.SkippedAction != 0 {
		t.Fatalf("rep = %+v, want actionCompared=1 skipped=0 skippedAction=0", rep)
	}
}

func TestRescoreToolsReconstructed(t *testing.T) {
	msgs := []mind.CompletionMessage{{Role: "user", Content: "q"}}
	tools, _ := json.Marshal([]mind.ToolDefinition{{Name: "bash", Description: "run", InputSchema: map[string]any{"type": "object"}}})
	sessions := []Session{sessionWith(
		agent.ProviderExchange{SessionID: "s1", ResponseText: "a", MessagesJSON: msgsJSON(t, msgs), ToolsJSON: tools, ToolChoice: "auto", ThinkingBudget: 256},
	)}
	cand := &stubCandidate{text: "a2"}
	judge := &stubJudge{reply: `{"score":75}`}

	if _, err := Rescore(context.Background(), sessions, cand, judge, RescoreOptions{}, fixedClock()); err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	if len(cand.requests) != 1 {
		t.Fatalf("expected one candidate call: %+v", cand.requests)
	}
	req := cand.requests[0]
	if len(req.Tools) != 1 || req.Tools[0].Name != "bash" {
		t.Fatalf("tools not reconstructed: %+v", req.Tools)
	}
	if req.ToolChoice != "auto" || req.ThinkingBudget != 256 {
		t.Fatalf("tool choice / thinking budget not carried: %+v", req)
	}
}

func TestRescoreCapExcludesDecodeErrors(t *testing.T) {
	// Two garbage-message rows then two good rows; cap=2 must still make 2 candidate
	// calls (the decode errors do not consume the budget).
	bad := json.RawMessage(`{not json`)
	good := msgsJSON(t, []mind.CompletionMessage{{Role: "user", Content: "q"}})
	sessions := []Session{sessionWith(
		agent.ProviderExchange{SessionID: "s1", Iteration: 0, ResponseText: "a", MessagesJSON: bad},
		agent.ProviderExchange{SessionID: "s1", Iteration: 1, ResponseText: "a", MessagesJSON: bad},
		agent.ProviderExchange{SessionID: "s1", Iteration: 2, ResponseText: "a", MessagesJSON: good},
		agent.ProviderExchange{SessionID: "s1", Iteration: 3, ResponseText: "a", MessagesJSON: good},
	)}
	cand := &stubCandidate{text: "c"}
	judge := &stubJudge{reply: `{"score":60}`}

	rep, err := Rescore(context.Background(), sessions, cand, judge, RescoreOptions{MaxExchanges: 2}, fixedClock())
	if err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	if rep.Compared != 2 {
		t.Fatalf("compared=%d, want 2 (decode errors must not consume the cap)", rep.Compared)
	}
	if rep.Errors != 2 {
		t.Fatalf("errors=%d, want 2", rep.Errors)
	}
}

func TestRescoreRequiresDeps(t *testing.T) {
	if _, err := Rescore(context.Background(), nil, nil, &stubJudge{}, RescoreOptions{}, fixedClock()); err == nil {
		t.Fatal("nil candidate must error")
	}
	if _, err := Rescore(context.Background(), nil, &stubCandidate{}, nil, RescoreOptions{}, fixedClock()); err == nil {
		t.Fatal("nil judge must error")
	}
}

func TestRescoreCandidateUsageAndEfficiency(t *testing.T) {
	msgs := []mind.CompletionMessage{{Role: "user", Content: "ping?"}}
	// Two scorable exchanges so the per-exchange normalization is exercised.
	sessions := []Session{sessionWith(
		agent.ProviderExchange{SessionID: "s1", Iteration: 0, ResponseText: "pong.", MessagesJSON: msgsJSON(t, msgs), DurationMs: 10},
		agent.ProviderExchange{SessionID: "s1", Iteration: 1, ResponseText: "pong.", MessagesJSON: msgsJSON(t, msgs), DurationMs: 10},
	)}
	// Cache tokens are real tokens and must count toward the per-1k denominator.
	cand := &stubCandidate{text: "pong!", usage: mind.Usage{InputTokens: 300, OutputTokens: 100, CacheReadTokens: 100}}
	judge := &stubJudge{reply: `{"score":80,"regression":false,"note":"ok"}`}

	rep, err := Rescore(context.Background(), sessions, cand, judge, RescoreOptions{Model: "m"}, fixedClock())
	if err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	// Candidate-only usage accumulates per successful re-run (2 calls × 500 tokens).
	if rep.CandidateUsage.InputTokens != 600 || rep.CandidateUsage.OutputTokens != 200 || rep.CandidateUsage.CacheReadTokens != 200 {
		t.Fatalf("candidate usage = %+v, want in=600 out=200 cacheRead=200", rep.CandidateUsage)
	}
	// avg_score=80 over 2 exchanges, 1000 total candidate tokens (incl. cache) →
	// 500 tok/exchange → 80 / (500/1000) = 160.0 quality points per 1k tokens.
	if got := rep.QualityPer1kTokens(); got != 160.0 {
		t.Fatalf("QualityPer1kTokens = %v, want 160.0", got)
	}
}

func TestRescoreCountsCandidateUsageWhenJudgeFails(t *testing.T) {
	// The candidate's tokens are spent the moment it generates, before the judge
	// runs — so a judge failure must still bill the candidate's usage even though
	// the exchange is uncompared. (Guards the placement of CandidateUsage.Add.)
	msgs := []mind.CompletionMessage{{Role: "user", Content: "ping?"}}
	sessions := []Session{sessionWith(
		agent.ProviderExchange{SessionID: "s1", Iteration: 0, ResponseText: "pong.", MessagesJSON: msgsJSON(t, msgs), DurationMs: 10},
	)}
	cand := &stubCandidate{text: "pong!", usage: mind.Usage{InputTokens: 300, OutputTokens: 50}}
	judge := &stubJudge{err: errors.New("judge offline")}

	rep, err := Rescore(context.Background(), sessions, cand, judge, RescoreOptions{Model: "m"}, fixedClock())
	if err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	if rep.Compared != 0 || rep.Errors != 1 {
		t.Fatalf("counts = compared=%d errors=%d, want compared=0 errors=1", rep.Compared, rep.Errors)
	}
	if rep.CandidateUsage.InputTokens != 300 || rep.CandidateUsage.OutputTokens != 50 {
		t.Fatalf("candidate usage = %+v, want in=300 out=50 despite judge error", rep.CandidateUsage)
	}
	// No compared exchanges → efficiency is zero (and must not divide by zero).
	if got := rep.QualityPer1kTokens(); got != 0 {
		t.Fatalf("QualityPer1kTokens = %v, want 0 with nothing compared", got)
	}
}

func TestQualityPer1kTokensZeroWhenNoTokens(t *testing.T) {
	// No compared exchanges, or unknown token usage, must not divide by zero.
	if got := (RescoreReport{Compared: 0, AvgScore: 90}).QualityPer1kTokens(); got != 0 {
		t.Fatalf("zero-compared efficiency = %v, want 0", got)
	}
	if got := (RescoreReport{Compared: 3, AvgScore: 90}).QualityPer1kTokens(); got != 0 {
		t.Fatalf("zero-token efficiency = %v, want 0", got)
	}
}

// fixedClock advances 10ms per call so CandidateMs is a stable positive value.
func fixedClock() func() time.Time {
	base := time.Unix(1_700_000_000, 0)
	n := 0
	return func() time.Time {
		n++
		return base.Add(time.Duration(n) * 10 * time.Millisecond)
	}
}
