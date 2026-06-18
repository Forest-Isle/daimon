package attention

import "strings"

// CanaryEvent is a recorded event paired with its ground-truth attention action
// (a user correction where one exists, otherwise the action the router took).
type CanaryEvent struct {
	Source      string
	Kind        string
	Payload     string
	GroundTruth Action
}

// RuleRejection records why a candidate rule was withheld by the canary.
type RuleRejection struct {
	Rule        Rule
	EventSource string
	EventKind   string
	RuleAction  Action
	GroundTruth Action
}

// ruleMatches reports whether rule r applies to an event with the given
// source/kind/payload, mirroring RulesRouter.Route's matching exactly: empty
// Source or Kind is a wildcard, a non-empty Contains must be a payload substring.
func ruleMatches(r Rule, source, kind, payload string) bool {
	if r.Source != "" && r.Source != source {
		return false
	}
	if r.Kind != "" && r.Kind != kind {
		return false
	}
	if r.Contains != "" && !strings.Contains(payload, r.Contains) {
		return false
	}
	return true
}

// ScreenRules is the deterministic attention-rule canary. It keeps only candidate
// rules that never downgrade a corpus event below its ground-truth action, and
// rejects the rest. A downgrade is candidate action < ground truth
// (Ignore<Reflex<Cognize<WakeUser); downgrading a WakeUser event is the
// zero-tolerance case (north-star #7). It is fail-closed: a candidate whose action
// does not parse, or which downgrades any matched event, is rejected. Pure: no DB,
// no model, no side effects.
func ScreenRules(corpus []CanaryEvent, candidates []Rule) (safe []Rule, rejected []RuleRejection) {
	for _, c := range candidates {
		act, err := ParseAction(c.Action)
		if err != nil {
			rejected = append(rejected, RuleRejection{Rule: c})
			continue
		}
		bad := false
		for _, ev := range corpus {
			if !ruleMatches(c, ev.Source, ev.Kind, ev.Payload) {
				continue
			}
			if act < ev.GroundTruth {
				rejected = append(rejected, RuleRejection{
					Rule:        c,
					EventSource: ev.Source,
					EventKind:   ev.Kind,
					RuleAction:  act,
					GroundTruth: ev.GroundTruth,
				})
				bad = true
				break
			}
		}
		if !bad {
			safe = append(safe, c)
		}
	}
	return safe, rejected
}
