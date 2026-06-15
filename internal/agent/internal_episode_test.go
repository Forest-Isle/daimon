package agent

import (
	"context"
	"testing"

	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/tool"
)

// fakeKernel records the request it was given and returns a canned outcome.
type fakeKernel struct {
	req     CognitiveRequest
	outcome CognitiveOutcome
	err     error
	called  bool
}

func (f *fakeKernel) Execute(_ context.Context, req CognitiveRequest) (CognitiveOutcome, error) {
	f.called = true
	f.req = req
	return f.outcome, f.err
}

// TestRunInternalEpisode_KernelDisabled: with no kernel wired, an autonomous
// trigger errors rather than silently doing nothing (no legacy fallback — there
// is no user waiting).
func TestRunInternalEpisode_KernelDisabled(t *testing.T) {
	deps := AgentDeps{Core: CoreDeps{AgentID: "t"}}.WithDefaults()
	a := NewAgent(&deps, &LinearLoop{}, NewEventBus())

	if _, err := a.RunInternalEpisode(context.Background(), "", "goal", "trigger", "internal.heartbeat"); err == nil {
		t.Fatal("expected error when kernel is unavailable")
	}
}

// TestRunInternalEpisode_HappyPath: a channel-less episode builds a well-formed
// request (goal/trigger/model/transcript/invoke), runs the kernel, and returns
// its outcome. The request carries no channel — replies land in the journal via
// the kernel, not a chat send.
func TestRunInternalEpisode_HappyPath(t *testing.T) {
	db := newTestDB(t)
	deps := AgentDeps{
		Core: CoreDeps{
			Tools:    tool.NewRegistry(),
			Sessions: session.NewManager(db),
			DB:       db,
			Cfg:      config.AgentConfig{Personality: "steady", PersistentRules: "be careful"},
			LLMCfg:   config.LLMConfig{Model: "test-model", Provider: "claude"},
		},
	}.WithDefaults()
	a := NewAgent(&deps, &LinearLoop{}, NewEventBus())

	fake := &fakeKernel{outcome: CognitiveOutcome{Status: "done", Summary: "reviewed commitments"}}
	a.SetKernel(fake, true)

	out, err := a.RunInternalEpisode(context.Background(), "evt-123", "Review commitments", "heartbeat", "internal.heartbeat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fake.called {
		t.Fatal("kernel was not invoked")
	}
	if fake.req.EpisodeID != "evt-123" {
		t.Fatalf("idempotency key not threaded to kernel: got %q", fake.req.EpisodeID)
	}
	if out.Status != "done" {
		t.Fatalf("want outcome status done, got %q", out.Status)
	}

	r := fake.req
	if r.Goal != "Review commitments" {
		t.Errorf("goal not passed through: %q", r.Goal)
	}
	if r.Trigger != "heartbeat" {
		t.Errorf("trigger not passed through: %q", r.Trigger)
	}
	if r.SessionID == "" {
		t.Error("session id should be set (internal session)")
	}
	if r.Model != "test-model" || r.Provider != "claude" {
		t.Errorf("model/provider not passed: model=%q provider=%q", r.Model, r.Provider)
	}
	if r.ActivityClass != "internal.heartbeat" {
		t.Errorf("activity class not threaded to kernel: got %q", r.ActivityClass)
	}
	if r.Persona != "steady" || r.Rules != "be careful" {
		t.Errorf("persona/rules not passed: persona=%q rules=%q", r.Persona, r.Rules)
	}
	if len(r.Transcript) != 1 || r.Transcript[0].Role != "user" || r.Transcript[0].Content != "heartbeat" {
		t.Errorf("transcript not built from trigger: %+v", r.Transcript)
	}
	if r.Invoke == nil {
		t.Error("invoke closure must be set so the kernel can run tools")
	}
}
