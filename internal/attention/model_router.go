package attention

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Forest-Isle/daimon/internal/heart"
	"github.com/Forest-Isle/daimon/internal/mind"
)

// LLMModelRouter is the small-model triage tier. It asks a cheap model to
// classify events the rules did not cover. It abstains (decided=false) on any
// error or unparseable response, so the chain falls through to Cognize rather
// than risk dropping an event on a model hiccup.
type LLMModelRouter struct {
	provider mind.Provider
	model    string
	cost     RouteCostSink
	// context returns a short digest of current commitments/state to ground the
	// triage. Optional; nil yields an empty digest.
	context func(ctx context.Context) string
}

// RouteCostSink records the token cost of one model-router call. Optional: a
// nil sink (the default) disables routing cost accounting, leaving routing
// behavior unchanged.
type RouteCostSink interface {
	RecordRouteCost(ctx context.Context, model string, usage mind.Usage)
}

func NewLLMModelRouter(provider mind.Provider, model string, contextFn func(ctx context.Context) string) *LLMModelRouter {
	return &LLMModelRouter{provider: provider, model: model, context: contextFn}
}

// SetCostSink wires the economy route-cost ledger. Optional: a nil sink (the
// default) disables route-cost accounting with no behavior change.
func (m *LLMModelRouter) SetCostSink(s RouteCostSink) { m.cost = s }

const modelRouterSystem = `You are the attention router for a personal agent. Decide how to handle one incoming event. Reply with ONLY JSON: {"action": "ignore|reflex|cognize|wake_user", "priority": 0-3, "reason": "..."}.
- ignore: noise, no action needed.
- cognize: needs the agent to think (default when unsure).
- wake_user: urgent, the user must be told now.
- reflex: a known routine handles it (rare from the model).
Bias toward cognize/wake_user over ignore — missing something important is worse than over-attending. priority 0 is urgent, 3 is idle/batch.`

func (m *LLMModelRouter) Route(ctx context.Context, ev heart.Event) (Verdict, bool) {
	if m == nil || m.provider == nil {
		return Verdict{}, false
	}

	var digest string
	if m.context != nil {
		digest = strings.TrimSpace(m.context(ctx))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Event source: %s\nEvent kind: %s\nPayload: %s", ev.Source, ev.Kind, ev.Payload)
	if digest != "" {
		fmt.Fprintf(&b, "\n\nCurrent commitments:\n%s", digest)
	}

	resp, err := m.provider.Complete(ctx, mind.CompletionRequest{
		Model:          m.model,
		System:         modelRouterSystem,
		Messages:       []mind.CompletionMessage{{Role: "user", Content: b.String()}},
		MaxTokens:      256,
		ResponseFormat: &mind.ResponseFormat{Type: "json_object"},
	})
	if err != nil || resp == nil {
		return Verdict{}, false
	}
	m.recordCost(ctx, resp.Usage)

	var parsed struct {
		Action   string `json:"action"`
		Priority int    `json:"priority"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(resp.Text), &parsed); err != nil {
		return Verdict{}, false
	}
	action, err := ParseAction(parsed.Action)
	if err != nil {
		return Verdict{}, false
	}
	return Verdict{Action: action, Priority: parsed.Priority, Reason: "model: " + strings.TrimSpace(parsed.Reason)}, true
}

func (m *LLMModelRouter) recordCost(ctx context.Context, usage mind.Usage) {
	if m.cost == nil || usage.InputTokens+usage.OutputTokens+usage.CacheReadTokens+usage.CacheCreationTokens <= 0 {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			slog.Warn("attention: route cost recorder panicked (ignored)", "model", m.model, "panic", rec)
		}
	}()
	m.cost.RecordRouteCost(ctx, m.model, usage)
}
