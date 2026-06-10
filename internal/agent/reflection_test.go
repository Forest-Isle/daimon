package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

func reflectionTestAgent(t *testing.T, maxReflections int) *Agent {
	t.Helper()
	deps := AgentDeps{
		Core: CoreDeps{
			Cfg:    config.AgentConfig{SystemPrompt: "test", MaxIterations: 5, MaxReflections: maxReflections},
			LLMCfg: config.LLMConfig{Model: "test"},
			Tools:  tool.NewRegistry(),
		},
		Memory:     MemoryDeps{}.WithDefaults(),
		Security:   SecurityDeps{}.WithDefaults(),
		MultiAgent: MultiAgentDeps{}.WithDefaults(),
	}
	return NewAgent(&deps, &LinearLoop{}, NewEventBus())
}

func sessionWithPlan(planJSON string) *session.Session {
	return &session.Session{Metadata: map[string]string{"plan": planJSON}}
}

func TestIncompletePlanSteps(t *testing.T) {
	tests := []struct {
		name string
		plan string
		want int
	}{
		{"nil metadata", "", 0},
		{"all done", `{"goal":"g","steps":[{"id":"1","status":"done"},{"id":"2","status":"done"}]}`, 0},
		{"one pending", `{"goal":"g","steps":[{"id":"1","status":"done"},{"id":"2","status":"pending"}]}`, 1},
		{"in_progress + pending", `{"goal":"g","steps":[{"id":"1","status":"in_progress"},{"id":"2","status":"pending"}]}`, 2},
		{"failed counts as resolved", `{"goal":"g","steps":[{"id":"1","status":"failed"},{"id":"2","status":"done"}]}`, 0},
		{"corrupt json", `not json`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sess *session.Session
			if tt.name == "nil metadata" {
				sess = &session.Session{Metadata: map[string]string{}}
			} else {
				sess = sessionWithPlan(tt.plan)
			}
			got := incompletePlanSteps(sess)
			if len(got) != tt.want {
				t.Errorf("incompletePlanSteps() = %d, want %d", len(got), tt.want)
			}
		})
	}
}

func TestMaybeReflect_NoPlanNoReflection(t *testing.T) {
	a := reflectionTestAgent(t, 3)
	sess := &session.Session{Metadata: map[string]string{}}
	if got := a.maybeReflect(sess, 0); got != "" {
		t.Errorf("expected no reflection without a plan, got: %q", got)
	}
}

func TestMaybeReflect_IncompletePlanTriggers(t *testing.T) {
	a := reflectionTestAgent(t, 3)
	sess := sessionWithPlan(`{"goal":"Fix bug","steps":[{"id":"1","description":"Add test","criteria":"test_run passes","status":"in_progress"}]}`)
	prompt := a.maybeReflect(sess, 0)
	if prompt == "" {
		t.Fatal("expected reflection prompt for incomplete plan")
	}
	if !strings.Contains(prompt, "Add test") || !strings.Contains(prompt, "test_run passes") {
		t.Errorf("reflection prompt should cite the incomplete step + criteria, got: %s", prompt)
	}
}

func TestMaybeReflect_BudgetExhausted(t *testing.T) {
	a := reflectionTestAgent(t, 2)
	sess := sessionWithPlan(`{"goal":"g","steps":[{"id":"1","status":"pending"}]}`)
	if got := a.maybeReflect(sess, 2); got != "" {
		t.Error("expected no reflection once budget is exhausted")
	}
	if got := a.maybeReflect(sess, 1); got == "" {
		t.Error("expected reflection while budget remains")
	}
}

func TestMaybeReflect_NegativeBudgetDisables(t *testing.T) {
	a := reflectionTestAgent(t, -1)
	sess := sessionWithPlan(`{"goal":"g","steps":[{"id":"1","status":"pending"}]}`)
	if got := a.maybeReflect(sess, 0); got != "" {
		t.Error("negative MaxReflections should disable reflection")
	}
}

func TestMaybeReflect_ZeroBudgetUsesDefault(t *testing.T) {
	a := reflectionTestAgent(t, 0) // 0 → default (3)
	sess := sessionWithPlan(`{"goal":"g","steps":[{"id":"1","status":"pending"}]}`)
	if got := a.maybeReflect(sess, 2); got == "" {
		t.Error("with default budget 3, reflection should still fire at attempt 2")
	}
	if got := a.maybeReflect(sess, 3); got != "" {
		t.Error("with default budget 3, reflection should stop at attempt 3")
	}
}

// reflectCountingProvider always returns text with no tool calls, counting
// how many times the loop invokes it.
type reflectCountingProvider struct{ calls int }

func (p *reflectCountingProvider) Complete(_ context.Context, _ CompletionRequest) (*CompletionResponse, error) {
	p.calls++
	return &CompletionResponse{Text: "done", StopReason: StopEndTurn}, nil
}

func (p *reflectCountingProvider) Stream(_ context.Context, _ CompletionRequest) (StreamIterator, error) {
	p.calls++
	return &testStream{text: "done"}, nil
}

// TestLinearLoop_ReflectionContinuesPastConvergence proves the loop re-engages
// the model when it converges with an incomplete plan, bounded by the budget.
func TestLinearLoop_ReflectionContinuesPastConvergence(t *testing.T) {
	sess := &session.Session{
		ID: "reflect-sess", Channel: "test", ChannelID: "ch1", CreatedAt: time.Now(),
		Metadata: map[string]string{
			"plan": `{"goal":"Fix bug","steps":[{"id":"1","description":"Add test","criteria":"test_run passes","status":"in_progress"}]}`,
		},
	}

	prov := &reflectCountingProvider{}
	deps := AgentDeps{}.WithDefaults()
	deps.Core.Tools = tool.NewRegistry()
	deps.Core.Cfg.MaxIterations = 20
	deps.Core.Cfg.MaxReflections = 3
	deps.Core.Provider = prov

	a := NewAgent(&deps, &LinearLoop{}, NewEventBus())
	loop := &LinearLoop{}
	err := loop.Execute(context.Background(), a, &testChannel{}, channel.InboundMessage{
		Channel: "test", ChannelID: "ch1", Text: "fix the bug",
	}, sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Plan stays incomplete, so the loop should reflect exactly MaxReflections
	// times: 1 initial convergence + 3 reflections = 4 provider calls.
	if prov.calls != 4 {
		t.Errorf("expected 4 provider calls (1 + 3 reflections), got %d", prov.calls)
	}

	// Each reflection appends a self-critique user message.
	reflectionMsgs := 0
	for _, m := range sess.History() {
		if m.Role == "user" && strings.Contains(m.Content, "[Self-check]") {
			reflectionMsgs++
		}
	}
	if reflectionMsgs != 3 {
		t.Errorf("expected 3 self-check messages, got %d", reflectionMsgs)
	}
}

// TestLinearLoop_NoReflectionWhenPlanComplete confirms zero behavior change
// when the plan is complete (or absent).
func TestLinearLoop_NoReflectionWhenPlanComplete(t *testing.T) {
	sess := &session.Session{
		ID: "no-reflect-sess", Channel: "test", ChannelID: "ch1", CreatedAt: time.Now(),
		Metadata: map[string]string{
			"plan": `{"goal":"Done","steps":[{"id":"1","status":"done"}]}`,
		},
	}

	prov := &reflectCountingProvider{}
	deps := AgentDeps{}.WithDefaults()
	deps.Core.Tools = tool.NewRegistry()
	deps.Core.Cfg.MaxIterations = 20
	deps.Core.Cfg.MaxReflections = 3
	deps.Core.Provider = prov

	a := NewAgent(&deps, &LinearLoop{}, NewEventBus())
	loop := &LinearLoop{}
	if err := loop.Execute(context.Background(), a, &testChannel{}, channel.InboundMessage{
		Channel: "test", ChannelID: "ch1", Text: "anything",
	}, sess); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Converges immediately, no reflection.
	if prov.calls != 1 {
		t.Errorf("expected 1 provider call (no reflection), got %d", prov.calls)
	}
}
