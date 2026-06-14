package sleep

import (
	"context"
	"errors"
	"testing"

	"github.com/Forest-Isle/daimon/internal/attention"
)

type fakeCorrections struct {
	items []RoutingCorrection
	err   error
}

func (f fakeCorrections) Corrections(_ context.Context, _ int) ([]RoutingCorrection, error) {
	return f.items, f.err
}

type fakeRuleSink struct {
	existing  []attention.Rule
	appended  []attention.Rule
	appendErr error
}

func (f *fakeRuleSink) Existing(_ context.Context) ([]attention.Rule, error) { return f.existing, nil }
func (f *fakeRuleSink) Append(_ context.Context, rules []attention.Rule) error {
	if f.appendErr != nil {
		return f.appendErr
	}
	f.appended = append(f.appended, rules...)
	return nil
}

func corr(eventID, source, kind, expected string) RoutingCorrection {
	return RoutingCorrection{EventID: eventID, Source: source, Kind: kind, Expected: expected}
}

func TestSynthesizeRequiresRepetition(t *testing.T) {
	sink := &fakeRuleSink{}
	job := NewSynthesizeRulesJob(fakeCorrections{items: []RoutingCorrection{
		corr("e1", "telegram", "message", "ignore"),
	}}, sink)

	msg, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "no new rules to synthesize" {
		t.Fatalf("a single correction must not synthesize a rule, got %q", msg)
	}
	if len(sink.appended) != 0 {
		t.Fatalf("nothing should be appended, got %+v", sink.appended)
	}
}

func TestSynthesizeUnanimousRepeatedRule(t *testing.T) {
	sink := &fakeRuleSink{}
	job := NewSynthesizeRulesJob(fakeCorrections{items: []RoutingCorrection{
		corr("e1", "telegram", "message", "ignore"),
		corr("e2", "telegram", "message", "ignore"),
	}}, sink)

	msg, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "synthesized 1 rule(s) (effective next restart)" {
		t.Fatalf("unexpected summary: %q", msg)
	}
	if len(sink.appended) != 1 {
		t.Fatalf("want 1 appended rule, got %+v", sink.appended)
	}
	r := sink.appended[0]
	if r.Source != "telegram" || r.Kind != "message" || r.Action != "ignore" {
		t.Fatalf("synthesized wrong rule: %+v", r)
	}
}

func TestSynthesizeCountsDistinctEvents(t *testing.T) {
	sink := &fakeRuleSink{}
	// Same event corrected twice is a single signal, not repetition.
	job := NewSynthesizeRulesJob(fakeCorrections{items: []RoutingCorrection{
		corr("e1", "telegram", "message", "ignore"),
		corr("e1", "telegram", "message", "ignore"),
	}}, sink)

	msg, _ := job.Run(context.Background())
	if msg != "no new rules to synthesize" {
		t.Fatalf("duplicate feedback for one event must not count as repetition, got %q", msg)
	}
}

func TestSynthesizeSkipsConflicting(t *testing.T) {
	got := synthesizeRules([]RoutingCorrection{
		corr("e1", "telegram", "message", "ignore"),
		corr("e2", "telegram", "message", "cognize"),
	}, nil)
	if len(got) != 0 {
		t.Fatalf("conflicting corrections must not synthesize a rule, got %+v", got)
	}
}

func TestSynthesizeSkipsCovered(t *testing.T) {
	existing := []attention.Rule{{Source: "telegram", Kind: "message", Action: "cognize"}}
	got := synthesizeRules([]RoutingCorrection{
		corr("e1", "telegram", "message", "ignore"),
		corr("e2", "telegram", "message", "ignore"),
	}, existing)
	if len(got) != 0 {
		t.Fatalf("a covered source/kind must be skipped, got %+v", got)
	}
}

func TestSynthesizeContainsRuleDoesNotCover(t *testing.T) {
	// A substring-scoped existing rule does not fully cover the source/kind.
	existing := []attention.Rule{{Source: "telegram", Kind: "message", Contains: "urgent", Action: "wake_user"}}
	got := synthesizeRules([]RoutingCorrection{
		corr("e1", "telegram", "message", "ignore"),
		corr("e2", "telegram", "message", "ignore"),
	}, existing)
	if len(got) != 1 {
		t.Fatalf("a Contains-scoped rule must not block synthesis, got %+v", got)
	}
}

func TestSynthesizeSkipsReflex(t *testing.T) {
	// A reflex rule needs a ReflexID that feedback cannot supply.
	got := synthesizeRules([]RoutingCorrection{
		corr("e1", "telegram", "message", "reflex"),
		corr("e2", "telegram", "message", "reflex"),
	}, nil)
	if len(got) != 0 {
		t.Fatalf("reflex corrections must not synthesize a rule, got %+v", got)
	}
}

func TestSynthesizeWildcardRuleCovers(t *testing.T) {
	// An existing wildcard-source rule for kind=message already routes any source.
	existing := []attention.Rule{{Kind: "message", Action: "cognize"}}
	got := synthesizeRules([]RoutingCorrection{
		corr("e1", "telegram", "message", "ignore"),
		corr("e2", "telegram", "message", "ignore"),
	}, existing)
	if len(got) != 0 {
		t.Fatalf("a wildcard-source rule must cover the candidate, got %+v", got)
	}
}

func TestSynthesizeMalformedExistingDoesNotCover(t *testing.T) {
	// An existing rule with an unparseable action is skipped at runtime, so it
	// covers nothing — a valid synthesized rule must still be produced.
	existing := []attention.Rule{{Source: "telegram", Kind: "message", Action: "bogus"}}
	got := synthesizeRules([]RoutingCorrection{
		corr("e1", "telegram", "message", "ignore"),
		corr("e2", "telegram", "message", "ignore"),
	}, existing)
	if len(got) != 1 {
		t.Fatalf("a malformed existing rule must not suppress synthesis, got %+v", got)
	}
}

func TestSynthesizeIgnoresInvalidAction(t *testing.T) {
	got := synthesizeRules([]RoutingCorrection{
		corr("e1", "telegram", "message", "frobnicate"),
		corr("e2", "telegram", "message", "frobnicate"),
	}, nil)
	if len(got) != 0 {
		t.Fatalf("unknown action must be ignored, got %+v", got)
	}
}

func TestSynthesizeEmptyCorrections(t *testing.T) {
	sink := &fakeRuleSink{}
	job := NewSynthesizeRulesJob(fakeCorrections{items: nil}, sink)
	msg, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "no corrections to learn from" {
		t.Fatalf("unexpected summary: %q", msg)
	}
}

func TestSynthesizeCorrectionSourceErrorPropagates(t *testing.T) {
	job := NewSynthesizeRulesJob(fakeCorrections{err: errors.New("db down")}, &fakeRuleSink{})
	if _, err := job.Run(context.Background()); err == nil {
		t.Fatal("correction source error should propagate")
	}
}
