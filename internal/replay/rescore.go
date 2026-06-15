package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Forest-Isle/daimon/internal/mind"
)

// Candidate is the minimal provider slice the re-scorer drives: it re-runs a
// recorded request against a candidate configuration. mind.Provider satisfies
// it, so `daimon replay --against <config>` passes the candidate's real provider;
// tests pass a stub.
type Candidate interface {
	Complete(ctx context.Context, req mind.CompletionRequest) (*mind.CompletionResponse, error)
}

// Judge scores a candidate response against the recorded baseline for the same
// context. It is an LLM (a small/haiku tier model is enough), kept to a thin
// Complete() boundary — the prompt construction and verdict parsing live in this
// package so the judge stays swappable and the scoring stays testable.
type Judge interface {
	Complete(ctx context.Context, systemPrompt, userMessage string) (string, error)
}

// Verdict is the judge's comparison of one candidate response to its baseline.
type Verdict struct {
	Score      int    `json:"score"`      // 0-100 candidate quality
	Regression bool   `json:"regression"` // candidate clearly worse than baseline
	Note       string `json:"note"`
	// Indeterminate is set by the harness (not the judge) when the judge's reply
	// could not be parsed into a verdict. The report still counts the exchange with
	// a neutral score for --against diagnostics, but the canary gate must treat an
	// indeterminate comparison as missing evidence, never as a clean pass.
	Indeterminate bool `json:"-"`
}

// RescoreOptions parameterizes a re-scoring run. MaxExchanges caps the total
// re-runs (a cost guard — each costs a candidate call plus a judge call); 0 means
// no cap. MaxTokens bounds each re-run's output; Model is stamped into the re-run
// request so the candidate provider uses the configuration under test.
type RescoreOptions struct {
	Model        string
	MaxTokens    int
	MaxExchanges int
}

// ExchangeResult is the outcome of re-scoring one recorded exchange.
type ExchangeResult struct {
	SessionID   string
	Iteration   int
	Baseline    string
	Candidate   string
	Verdict     Verdict
	BaselineMs  int64
	CandidateMs int64
	Err         string
}

// RescoreReport aggregates a re-scoring run. Skipped counts recorded exchanges
// not scored because they are not a fair text comparison: an empty baseline (a
// pure tool-call turn, no prose) or a turn whose baseline made tool calls (an
// action turn — judging it as text would misrepresent it; faithful action
// re-scoring is a later increment). Errors counts exchanges whose candidate or
// judge call failed. AvgScore is the mean judge score over scored exchanges.
type RescoreReport struct {
	Compared    int
	Regressions int
	Errors      int
	Skipped     int
	// SkippedAction is the subset of Skipped that were action turns (the baseline
	// made tool calls). They are skipped because faithful action re-scoring does
	// not exist yet — so the candidate's tool/action behavior went UNverified. The
	// canary gate uses this to refuse certifying a change over sessions whose action
	// behavior it could not check.
	SkippedAction int
	// Indeterminate is how many compared exchanges had an unparseable judge reply
	// (counted in Compared with a neutral score for diagnostics). The canary gate
	// fails when this is non-zero: a comparison the judge could not render is not
	// evidence the change is safe.
	Indeterminate int
	AvgScore      int
	// Capped is true when the run stopped at MaxExchanges with more scorable
	// exchanges left unscored — so the report is not mistaken for full coverage.
	Capped  bool
	Results []ExchangeResult
}

const rescoreJudgeSystemPrompt = `You are an offline replay judge for a personal agent. You are given the agent's CONTEXT (the last user turn), the BASELINE response that was actually produced in the recorded run, and a CANDIDATE response from a new configuration for the same context. Judge only the candidate's quality relative to the baseline for THIS context. Respond with ONLY a JSON object {"score":<integer 0-100>,"regression":<true|false>,"note":"<one short sentence>"} and nothing else. Set regression=true only when the candidate is clearly worse than the baseline (less correct, less helpful, or unsafe); a candidate that is as good or better is not a regression.`

// Rescore re-runs each recorded exchange through the candidate provider and asks
// the judge to compare the new response to the recorded baseline. It is the
// engine behind `daimon replay --against`: offline re-scoring (action dry-run —
// it only generates and judges text, it never executes tools or writes the
// world). Exchanges with an empty baseline (pure tool-call turns) are skipped;
// candidate/judge failures are recorded per-exchange and counted, never aborting
// the run. now is injected so the per-call latency the report attributes to the
// candidate is testable; a nil now uses the wall clock.
func Rescore(ctx context.Context, sessions []Session, cand Candidate, judge Judge, opts RescoreOptions, now func() time.Time) (RescoreReport, error) {
	if cand == nil || judge == nil {
		return RescoreReport{}, fmt.Errorf("rescore: candidate provider and judge are required")
	}
	if now == nil {
		now = time.Now
	}
	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	var rep RescoreReport
	scoreSum := 0
	attempts := 0 // candidate calls made; the cost guard counts these, not skips/decode errors
	for _, s := range sessions {
		for _, ex := range s.Exchanges {
			baseline := strings.TrimSpace(ex.ResponseText)
			if baseline == "" {
				rep.Skipped++ // no prose to compare
				// An empty-prose turn that still made tool calls IS an action turn
				// (the model called a tool and said nothing) — its action behavior
				// went unverified, so it must count toward SkippedAction too, not just
				// the benign empty-baseline case.
				if recordedToolCalls(ex.ToolCallsJSON) {
					rep.SkippedAction++
				}
				continue
			}
			if recordedToolCalls(ex.ToolCallsJSON) {
				rep.Skipped++       // action turn: text judging would misrepresent it
				rep.SkippedAction++ // ...and its action behavior went unverified
				continue
			}
			// Cap candidate calls (a real cost), not skipped/decode-error rows, so
			// garbage rows cannot exhaust the budget before the requested number of
			// re-runs happen.
			if opts.MaxExchanges > 0 && attempts >= opts.MaxExchanges {
				rep.Capped = true // more scorable exchanges remain unscored
				return finalize(rep, scoreSum), nil
			}

			messages, err := decodeMessages(ex.MessagesJSON)
			if err != nil {
				rep.Errors++
				rep.Results = append(rep.Results, ExchangeResult{
					SessionID: ex.SessionID, Iteration: ex.Iteration, Baseline: baseline,
					BaselineMs: ex.DurationMs, Err: "decode messages: " + err.Error(),
				})
				continue
			}
			// Re-present the same tool affordances and decoding contract the model saw,
			// so the candidate is judged under the recorded conditions; garbage tool
			// JSON degrades to no tools rather than failing the row.
			tools, _ := decodeTools(ex.ToolsJSON)

			attempts++
			start := now()
			resp, err := cand.Complete(ctx, mind.CompletionRequest{
				Model:          opts.Model,
				System:         ex.SystemPrompt,
				Messages:       messages,
				Tools:          tools,
				ToolChoice:     ex.ToolChoice,
				ThinkingBudget: ex.ThinkingBudget,
				MaxTokens:      maxTokens,
			})
			candidateMs := now().Sub(start).Milliseconds()
			if err != nil {
				rep.Errors++
				rep.Results = append(rep.Results, ExchangeResult{
					SessionID: ex.SessionID, Iteration: ex.Iteration, Baseline: baseline,
					BaselineMs: ex.DurationMs, CandidateMs: candidateMs, Err: "candidate: " + err.Error(),
				})
				continue
			}
			candidateText := strings.TrimSpace(resp.Text)

			verdict, err := judgeExchange(ctx, judge, lastUserTurn(messages), baseline, candidateText)
			if err != nil {
				rep.Errors++
				rep.Results = append(rep.Results, ExchangeResult{
					SessionID: ex.SessionID, Iteration: ex.Iteration, Baseline: baseline,
					Candidate: candidateText, BaselineMs: ex.DurationMs, CandidateMs: candidateMs,
					Err: "judge: " + err.Error(),
				})
				continue
			}

			rep.Compared++
			scoreSum += verdict.Score
			if verdict.Regression {
				rep.Regressions++
			}
			if verdict.Indeterminate {
				rep.Indeterminate++
			}
			rep.Results = append(rep.Results, ExchangeResult{
				SessionID: ex.SessionID, Iteration: ex.Iteration, Baseline: baseline,
				Candidate: candidateText, Verdict: verdict, BaselineMs: ex.DurationMs, CandidateMs: candidateMs,
			})
		}
	}
	return finalize(rep, scoreSum), nil
}

func finalize(rep RescoreReport, scoreSum int) RescoreReport {
	if rep.Compared > 0 {
		rep.AvgScore = scoreSum / rep.Compared
	}
	return rep
}

// judgeExchange asks the judge to compare one candidate response to its baseline
// and parses the verdict, degrading an unparseable reply to a neutral, non-
// regression verdict (a judge hiccup must not fabricate a regression).
func judgeExchange(ctx context.Context, judge Judge, contextTurn, baseline, candidate string) (Verdict, error) {
	var b strings.Builder
	b.WriteString("## Context (last user turn)\n")
	b.WriteString(truncate(contextTurn, 2000))
	b.WriteString("\n\n## Baseline response\n")
	b.WriteString(truncate(baseline, 2000))
	b.WriteString("\n\n## Candidate response\n")
	b.WriteString(truncate(candidate, 2000))

	raw, err := judge.Complete(ctx, rescoreJudgeSystemPrompt, b.String())
	if err != nil {
		return Verdict{}, err
	}
	v, ok := parseVerdict(raw)
	if !ok {
		// Unparseable judgment: neutral score, not a regression for diagnostics, but
		// flagged Indeterminate so the canary gate does not read it as a clean pass.
		return Verdict{Score: 50, Note: "unparseable judge reply", Indeterminate: true}, nil
	}
	if v.Score < 0 {
		v.Score = 0
	}
	if v.Score > 100 {
		v.Score = 100
	}
	return v, nil
}

// parseVerdict extracts the verdict object from the judge reply, tolerating code
// fences and surrounding prose by scanning balanced, string-aware top-level
// objects. A verdict is accepted only when BOTH required fields are present —
// score and regression. Pointer probes distinguish an absent regression key from
// an explicit false: a reply like {"score":90} (no regression) is schema-
// incomplete and must NOT be read as "not a regression", so it is rejected here
// and the caller records it as indeterminate rather than a clean pass.
func parseVerdict(raw string) (Verdict, bool) {
	for _, candidate := range jsonObjectSpans(raw) {
		if !strings.Contains(candidate, `"score"`) {
			continue
		}
		var probe struct {
			Score      *int   `json:"score"`
			Regression *bool  `json:"regression"`
			Note       string `json:"note"`
		}
		if json.Unmarshal([]byte(candidate), &probe) == nil && probe.Score != nil && probe.Regression != nil {
			return Verdict{Score: *probe.Score, Regression: *probe.Regression, Note: probe.Note}, true
		}
	}
	return Verdict{}, false
}

// jsonObjectSpans returns the balanced top-level {...} spans in s, in order,
// tracking JSON string state (and escapes) so braces inside string values do not
// throw off the depth count.
func jsonObjectSpans(s string) []string {
	var out []string
	depth, start := 0, -1
	inStr, esc := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth > 0 {
				depth--
				if depth == 0 && start >= 0 {
					out = append(out, s[start:i+1])
					start = -1
				}
			}
		}
	}
	return out
}

func decodeMessages(raw json.RawMessage) ([]mind.CompletionMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var messages []mind.CompletionMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func decodeTools(raw json.RawMessage) ([]mind.ToolDefinition, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var tools []mind.ToolDefinition
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil, err
	}
	return tools, nil
}

// recordedToolCalls reports whether the recorded exchange made any tool calls.
// The recorder marshals an empty slice as "[]", so a non-empty decode means the
// turn was an action turn, not a pure text answer.
func recordedToolCalls(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var calls []mind.ToolUseBlock
	if json.Unmarshal(raw, &calls) != nil {
		return false
	}
	return len(calls) > 0
}

// lastUserTurn returns the content of the last user message, the immediate prompt
// the judge needs as context. Empty when there is no user message with content.
func lastUserTurn(messages []mind.CompletionMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && strings.TrimSpace(messages[i].Content) != "" {
			return messages[i].Content
		}
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
