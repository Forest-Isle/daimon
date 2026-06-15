package attention

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/daimon/internal/heart"
	"github.com/Forest-Isle/daimon/internal/mind"
	"github.com/Forest-Isle/daimon/internal/store"
)

func TestActionStringParseRoundTrip(t *testing.T) {
	for _, a := range []Action{Ignore, Reflex, Cognize, WakeUser} {
		got, err := ParseAction(a.String())
		if err != nil || got != a {
			t.Fatalf("round-trip %v -> %q -> %v err=%v", a, a.String(), got, err)
		}
	}
	if _, err := ParseAction("nope"); err == nil {
		t.Fatal("ParseAction(nope) error = nil, want error")
	}
}

func TestRulesRouterMatching(t *testing.T) {
	r := NewRulesRouter([]Rule{
		{Source: "mail", Kind: "mail.received", Contains: "unsubscribe", Action: "ignore"},
		{Source: "mail", Kind: "mail.received", Action: "cognize", Priority: 1},
		{Kind: "tick", Action: "reflex", ReflexID: "daily_report"},
	})
	ctx := context.Background()

	v, ok := r.Route(ctx, heart.Event{Source: "mail", Kind: "mail.received", Payload: "click unsubscribe here"})
	if !ok || v.Action != Ignore {
		t.Fatalf("spam rule: ok=%v action=%v", ok, v.Action)
	}
	v, ok = r.Route(ctx, heart.Event{Source: "mail", Kind: "mail.received", Payload: "invoice attached"})
	if !ok || v.Action != Cognize || v.Priority != 1 {
		t.Fatalf("mail rule: ok=%v action=%v prio=%d", ok, v.Action, v.Priority)
	}
	v, ok = r.Route(ctx, heart.Event{Source: "timer", Kind: "tick"})
	if !ok || v.Action != Reflex || v.ReflexID != "daily_report" {
		t.Fatalf("tick rule: ok=%v action=%v reflex=%q", ok, v.Action, v.ReflexID)
	}
	if _, ok := r.Route(ctx, heart.Event{Source: "fs", Kind: "file.changed"}); ok {
		t.Fatal("unmatched event should not match")
	}
}

func TestChainFallsThroughToCognize(t *testing.T) {
	c := NewChain(NewRulesRouter(nil), nil)
	v, err := c.Route(context.Background(), heart.Event{Source: "unknown", Kind: "weird"})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if v.Action != Cognize {
		t.Fatalf("default action = %v, want Cognize (prefer to wake)", v.Action)
	}
}

func TestChainRulesBeatModel(t *testing.T) {
	rules := NewRulesRouter([]Rule{{Source: "mail", Action: "ignore"}})
	model := &stubModel{verdict: Verdict{Action: WakeUser}, decided: true}
	c := NewChain(rules, model)
	v, _ := c.Route(context.Background(), heart.Event{Source: "mail", Kind: "x"})
	if v.Action != Ignore {
		t.Fatalf("rules should win: action = %v", v.Action)
	}
	if model.called {
		t.Fatal("model should not be consulted when a rule matches")
	}
}

func TestChainUsesModelThenFallback(t *testing.T) {
	rules := NewRulesRouter(nil)

	decided := &stubModel{verdict: Verdict{Action: WakeUser, Priority: 0}, decided: true}
	if v, _ := NewChain(rules, decided).Route(context.Background(), heart.Event{Kind: "x"}); v.Action != WakeUser {
		t.Fatalf("model decision ignored: %v", v.Action)
	}

	abstain := &stubModel{decided: false}
	if v, _ := NewChain(rules, abstain).Route(context.Background(), heart.Event{Kind: "x"}); v.Action != Cognize {
		t.Fatalf("abstaining model should fall through to Cognize, got %v", v.Action)
	}
}

func TestLLMModelRouterParsesAndAbstains(t *testing.T) {
	ev := heart.Event{Source: "mail", Kind: "mail.received", Payload: "hi"}

	good := NewLLMModelRouter(&stubProvider{text: `{"action":"wake_user","priority":0,"reason":"urgent"}`}, "haiku", nil)
	if v, ok := good.Route(context.Background(), ev); !ok || v.Action != WakeUser {
		t.Fatalf("good parse: ok=%v action=%v", ok, v.Action)
	}

	bad := NewLLMModelRouter(&stubProvider{text: "not json"}, "haiku", nil)
	if _, ok := bad.Route(context.Background(), ev); ok {
		t.Fatal("unparseable response should abstain")
	}

	errp := NewLLMModelRouter(&stubProvider{err: errors.New("boom")}, "haiku", nil)
	if _, ok := errp.Route(context.Background(), ev); ok {
		t.Fatal("provider error should abstain")
	}
}

func TestFeedbackStore(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "att.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	fs := NewFeedbackStore(db.DB)
	ctx := context.Background()

	if err := fs.Record(ctx, Feedback{EventID: "e1", ExpectedAction: "wake_user", GivenAction: "ignore", Note: "this was important"}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	got, err := fs.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("Recent() error = %v", err)
	}
	if len(got) != 1 || got[0].ExpectedAction != "wake_user" {
		t.Fatalf("feedback = %#v", got)
	}
}

// --- stubs ---

type stubModel struct {
	verdict Verdict
	decided bool
	called  bool
}

func (s *stubModel) Route(_ context.Context, _ heart.Event) (Verdict, bool) {
	s.called = true
	return s.verdict, s.decided
}

type stubProvider struct {
	text string
	err  error
}

func (s *stubProvider) Complete(_ context.Context, _ mind.CompletionRequest) (*mind.CompletionResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &mind.CompletionResponse{Text: s.text}, nil
}

func (s *stubProvider) Stream(_ context.Context, _ mind.CompletionRequest) (mind.StreamIterator, error) {
	return nil, errors.New("not implemented")
}
