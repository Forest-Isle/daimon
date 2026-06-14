package replay

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/agent"
)

// stubCandidate returns a fixed response (or error) and records the requests it
// received, so the harness's request reconstruction can be asserted.
type stubCandidate struct {
	text     string
	err      error
	requests []agent.CompletionRequest
}

func (s *stubCandidate) Complete(_ context.Context, req agent.CompletionRequest) (*agent.CompletionResponse, error) {
	s.requests = append(s.requests, req)
	if s.err != nil {
		return nil, s.err
	}
	return &agent.CompletionResponse{Text: s.text, StopReason: agent.StopEndTurn}, nil
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

func msgsJSON(t *testing.T, msgs []agent.CompletionMessage) json.RawMessage {
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
	msgs := []agent.CompletionMessage{
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
	msgs := []agent.CompletionMessage{{Role: "user", Content: "help"}}
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
	msgs := []agent.CompletionMessage{{Role: "user", Content: "help"}}
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
	msgs := []agent.CompletionMessage{{Role: "user", Content: "help"}}
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
	msgs := []agent.CompletionMessage{{Role: "user", Content: "help"}}
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

func TestRescoreSkipsBaselineToolCallTurns(t *testing.T) {
	msgs := []agent.CompletionMessage{{Role: "user", Content: "do it"}}
	toolCalls, _ := json.Marshal([]agent.ToolUseBlock{{ID: "t1", Name: "bash", Input: "{}"}})
	sessions := []Session{sessionWith(
		// text + recorded tool call → action turn, skipped (text judging would misrepresent).
		agent.ProviderExchange{SessionID: "s1", ResponseText: "running it", MessagesJSON: msgsJSON(t, msgs), ToolCallsJSON: toolCalls},
		// pure text → scored.
		agent.ProviderExchange{SessionID: "s1", Iteration: 1, ResponseText: "answer", MessagesJSON: msgsJSON(t, msgs)},
	)}
	cand := &stubCandidate{text: "answer2"}
	judge := &stubJudge{reply: `{"score":80,"regression":false}`}

	rep, err := Rescore(context.Background(), sessions, cand, judge, RescoreOptions{}, fixedClock())
	if err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	if rep.Compared != 1 || rep.Skipped != 1 {
		t.Fatalf("rep = %+v, want compared=1 skipped=1", rep)
	}
}

func TestRescoreToolsReconstructed(t *testing.T) {
	msgs := []agent.CompletionMessage{{Role: "user", Content: "q"}}
	tools, _ := json.Marshal([]agent.ToolDefinition{{Name: "bash", Description: "run", InputSchema: map[string]any{"type": "object"}}})
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
	good := msgsJSON(t, []agent.CompletionMessage{{Role: "user", Content: "q"}})
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

// fixedClock advances 10ms per call so CandidateMs is a stable positive value.
func fixedClock() func() time.Time {
	base := time.Unix(1_700_000_000, 0)
	n := 0
	return func() time.Time {
		n++
		return base.Add(time.Duration(n) * 10 * time.Millisecond)
	}
}
