package sleep

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/Forest-Isle/daimon/internal/attention"
)

// synthesizeMinOccurrences is how many consistent corrections a (source, kind)
// must accumulate before a rule is synthesized. A single correction is treated as
// noise — only a repeated, unanimous correction becomes a durable rule, so one
// off decision cannot silently reshape routing.
const synthesizeMinOccurrences = 2

const synthesizeFeedbackLimit = 200

// RoutingCorrection is a user correction of how an event was routed, already
// joined with the corrected event's source/kind. Expected is the attention
// action the user said the router should have taken (an "ignore"/"cognize"/…
// string); a correction is only meaningful when expected differs from what the
// router actually did, which the boundary adapter filters before handing it here.
type RoutingCorrection struct {
	EventID  string
	Source   string
	Kind     string
	Expected string
}

// correctionSource yields recent routing corrections joined with event metadata.
// The join (feedback ⋈ events) lives in the adapter so this job stays pure logic.
type correctionSource interface {
	Corrections(ctx context.Context, limit int) ([]RoutingCorrection, error)
}

// ruleSink reads the existing attention rules and appends synthesized ones. The
// adapter owns the rules file; the job never touches the filesystem.
type ruleSink interface {
	Existing(ctx context.Context) ([]attention.Rule, error)
	Append(ctx context.Context, rules []attention.Rule) error
}

// canaryCorpus yields recorded events with ground-truth attention actions, for
// screening synthesized rules before they are appended. Optional: a nil corpus
// skips screening (preserves prior behavior for callers that do not wire one).
type canaryCorpus interface {
	CanaryEvents(ctx context.Context) ([]attention.CanaryEvent, error)
}

// SynthesizeRulesJob mines user routing corrections into deterministic attention
// rules, so a correction the user makes repeatedly stops costing a model/cognition
// call and becomes a cheap rule-tier decision. It is conservative by construction:
// it only synthesizes a rule for a (source, kind) whose corrections are unanimous
// and meet a repetition threshold, it never duplicates a rule already covering that
// (source, kind), and the synthesized action is the user's own stated expectation.
// The hard high-risk whitelist in attention.Chain still runs ahead of all rules,
// so a synthesized "ignore" can never drop a high-risk event.
type SynthesizeRulesJob struct {
	corrections correctionSource
	rules       ruleSink
	corpus      canaryCorpus
}

func NewSynthesizeRulesJob(c correctionSource, r ruleSink, corpus canaryCorpus) *SynthesizeRulesJob {
	return &SynthesizeRulesJob{corrections: c, rules: r, corpus: corpus}
}

func (j *SynthesizeRulesJob) Name() string { return "synthesize-rules" }

func (j *SynthesizeRulesJob) Run(ctx context.Context) (string, error) {
	if j.corrections == nil || j.rules == nil {
		return "", fmt.Errorf("synthesize-rules: correction source and rule sink are required")
	}

	corrections, err := j.corrections.Corrections(ctx, synthesizeFeedbackLimit)
	if err != nil {
		return "", fmt.Errorf("synthesize-rules: read corrections: %w", err)
	}
	if len(corrections) == 0 {
		return "no corrections to learn from", nil
	}
	existing, err := j.rules.Existing(ctx)
	if err != nil {
		return "", fmt.Errorf("synthesize-rules: read existing rules: %w", err)
	}

	candidates := synthesizeRules(corrections, existing)
	if len(candidates) == 0 {
		return "no new rules to synthesize", nil
	}
	if j.corpus != nil {
		corpus, err := j.corpus.CanaryEvents(ctx)
		if err != nil {
			// Fail closed: never append rules we could not screen.
			return fmt.Sprintf("canary corpus unavailable; withheld %d candidate rule(s)", len(candidates)), nil
		}
		if len(corpus) == 0 {
			// Fail closed: no recorded evidence to screen against.
			return fmt.Sprintf("canary corpus empty; withheld %d candidate rule(s)", len(candidates)), nil
		}
		safe, rejected := attention.ScreenRules(corpus, candidates)
		for _, rej := range rejected {
			slog.Warn("synthesize-rules: candidate rejected by canary",
				"source", rej.Rule.Source, "kind", rej.Rule.Kind,
				"rule_action", rej.RuleAction.String(), "ground_truth", rej.GroundTruth.String())
		}
		candidates = safe
		if len(candidates) == 0 {
			return fmt.Sprintf("all %d candidate rule(s) withheld by canary", len(rejected)), nil
		}
	}
	if err := j.rules.Append(ctx, candidates); err != nil {
		return "", fmt.Errorf("synthesize-rules: append rules: %w", err)
	}
	return fmt.Sprintf("synthesized %d rule(s) (effective next restart)", len(candidates)), nil
}

// synthesizeRules is the pure decision: group corrections by (source, kind),
// require a unanimous expected action meeting the repetition threshold, drop any
// (source, kind) already covered by an existing rule, and emit one rule each.
func synthesizeRules(corrections []RoutingCorrection, existing []attention.Rule) []attention.Rule {
	type group struct {
		source, kind string
		expected     string
		events       map[string]bool // distinct corrected events (repetition signal)
		conflict     bool
	}
	groups := map[string]*group{}
	order := []string{}
	for _, c := range corrections {
		if c.Source == "" || c.Kind == "" || c.Expected == "" || c.EventID == "" {
			continue // cannot key a rule without both selectors, an action, and an event
		}
		act, err := attention.ParseAction(c.Expected)
		if err != nil {
			continue // ignore corrections naming an unknown action
		}
		if act == attention.Reflex {
			// A reflex rule needs a concrete ReflexID, which feedback does not
			// capture; synthesizing one would route events to an empty handler.
			continue
		}
		key := c.Source + "\x00" + c.Kind
		g, ok := groups[key]
		if !ok {
			g = &group{source: c.Source, kind: c.Kind, expected: c.Expected, events: map[string]bool{}}
			groups[key] = g
			order = append(order, key)
		}
		if g.expected != c.Expected {
			g.conflict = true // user corrected the same source/kind two different ways
		}
		g.events[c.EventID] = true
	}

	var out []attention.Rule
	for _, key := range order {
		g := groups[key]
		// Count distinct events: correcting one event twice is a single signal.
		if g.conflict || len(g.events) < synthesizeMinOccurrences {
			continue
		}
		if isCovered(existing, g.source, g.kind) {
			continue // an existing rule already routes this source/kind; first match wins
		}
		out = append(out, attention.Rule{
			Source: g.source,
			Kind:   g.kind,
			Action: g.expected,
		})
	}
	sort.Slice(out, func(i, k int) bool {
		if out[i].Source != out[k].Source {
			return out[i].Source < out[k].Source
		}
		return out[i].Kind < out[k].Kind
	})
	return out
}

// isCovered reports whether an existing rule already routes (source, kind) at the
// rule tier, so a synthesized rule for it would be unreachable (first match wins)
// and must be skipped. It mirrors RulesRouter.Route's matching: empty Source/Kind
// are wildcards, a non-empty Contains only partially covers (so it does NOT
// suppress synthesis), and a rule with an unparseable action is ignored at
// runtime (so it covers nothing).
func isCovered(existing []attention.Rule, source, kind string) bool {
	for _, r := range existing {
		if r.Contains != "" {
			continue
		}
		if _, err := attention.ParseAction(r.Action); err != nil {
			continue
		}
		if (r.Source == "" || r.Source == source) && (r.Kind == "" || r.Kind == kind) {
			return true
		}
	}
	return false
}
