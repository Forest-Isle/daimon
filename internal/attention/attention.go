// Package attention decides whether an event is worth waking the expensive
// cognitive path for. It is the cost root of an always-on agent: most events are
// handled by cheap rules or a small model, and only a few earn a full episode.
// The bias is to over-wake — missing an important event costs far more than a
// wasted cognition — so an undecided event defaults to Cognize.
package attention

import (
	"context"
	"fmt"
	"strings"

	"github.com/Forest-Isle/daimon/internal/heart"
)

// Action is what the agent should do with an event.
type Action int

const (
	// Ignore drops the event.
	Ignore Action = iota
	// Reflex runs a pre-compiled deterministic handler (a skill/workflow).
	Reflex
	// Cognize spends a full cognitive episode.
	Cognize
	// WakeUser interrupts the user directly.
	WakeUser
)

func (a Action) String() string {
	switch a {
	case Ignore:
		return "ignore"
	case Reflex:
		return "reflex"
	case Cognize:
		return "cognize"
	case WakeUser:
		return "wake_user"
	default:
		return "unknown"
	}
}

// ParseAction converts a stored/config action string into an Action.
func ParseAction(s string) (Action, error) {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "ignore":
		return Ignore, nil
	case "reflex":
		return Reflex, nil
	case "cognize":
		return Cognize, nil
	case "wake_user":
		return WakeUser, nil
	default:
		return 0, fmt.Errorf("unknown attention action %q", s)
	}
}

// Verdict is the routing decision for one event.
type Verdict struct {
	Action   Action
	ReflexID string // set when Action == Reflex
	Priority int    // 0 urgent … 3 idle/batch
	Reason   string
}

// Router decides what to do with an event.
type Router interface {
	Route(ctx context.Context, ev heart.Event) (Verdict, error)
}

// Rule matches an event by source/kind and optional payload substring. Empty
// Source or Kind is a wildcard.
type Rule struct {
	Source   string `yaml:"source"`
	Kind     string `yaml:"kind"`
	Contains string `yaml:"contains"`
	Action   string `yaml:"action"`
	ReflexID string `yaml:"reflex_id"`
	Priority int    `yaml:"priority"`
}

// RulesRouter is the cheapest tier: deterministic, user-editable matching. It is
// also where the sleep phase writes synthesized rules.
type RulesRouter struct {
	rules []Rule
}

func NewRulesRouter(rules []Rule) *RulesRouter {
	return &RulesRouter{rules: rules}
}

// Route returns the first matching rule's verdict. matched is false when no rule
// applies, so the chain can fall through to the next tier.
func (r *RulesRouter) Route(_ context.Context, ev heart.Event) (Verdict, bool) {
	for _, rule := range r.rules {
		if !ruleMatches(rule, ev.Source, ev.Kind, ev.Payload) {
			continue
		}
		action, err := ParseAction(rule.Action)
		if err != nil {
			continue // a malformed rule must not silently swallow events
		}
		return Verdict{Action: action, ReflexID: rule.ReflexID, Priority: rule.Priority, Reason: "rule"}, true
	}
	return Verdict{}, false
}

// ModelRouter is the optional middle tier: a small model triages events the
// rules did not cover. decided is false when the model abstains or errors, so
// the chain falls through to the Cognize default.
type ModelRouter interface {
	Route(ctx context.Context, ev heart.Event) (verdict Verdict, decided bool)
}

// DefaultHighRiskKinds are event kinds that must always wake the user, ahead of
// any rule or model decision (constitution rule 4: irreversible/high-risk always
// human-signed). Matched by exact value or prefix, so "payment.charge" and
// "payment.refund" both qualify.
func DefaultHighRiskKinds() []string {
	return []string{"payment.", "security.", "legal.", "account.delete"}
}

// Chain runs the tiers cheapest-first: hard whitelist → rules → model → default
// Cognize. The hard whitelist is checked first and cannot be overridden by a
// (possibly synthesized) rule or the model, so high-risk events can never be
// down-routed to Ignore. The default is deliberately Cognize, not Ignore: an
// unclassified event is worth a thought rather than silently dropped.
type Chain struct {
	highRiskKinds []string
	rules         *RulesRouter
	model         ModelRouter
}

func NewChain(rules *RulesRouter, model ModelRouter) *Chain {
	if rules == nil {
		rules = NewRulesRouter(nil)
	}
	return &Chain{rules: rules, model: model}
}

// SetHighRiskKinds installs the always-wake whitelist. Kinds are matched by exact
// value or prefix. Call during setup, before the heart's Route loop starts: the
// whitelist is not protected for concurrent mutation (it is configured once and
// re-applied only on restart).
func (c *Chain) SetHighRiskKinds(kinds []string) { c.highRiskKinds = kinds }

func (c *Chain) isHighRisk(kind string) bool {
	for _, p := range c.highRiskKinds {
		if p == "" {
			continue
		}
		if kind == p || strings.HasPrefix(kind, p) {
			return true
		}
	}
	return false
}

func (c *Chain) Route(ctx context.Context, ev heart.Event) (Verdict, error) {
	// Hard whitelist first: high-risk kinds always wake the user and are never
	// delegated to rule or model routing.
	if c.isHighRisk(ev.Kind) {
		return Verdict{Action: WakeUser, Priority: 0, Reason: "high-risk kind: always wake"}, nil
	}
	if v, ok := c.rules.Route(ctx, ev); ok {
		return v, nil
	}
	if c.model != nil {
		if v, ok := c.model.Route(ctx, ev); ok {
			return v, nil
		}
	}
	return Verdict{Action: Cognize, Reason: "default: prefer to wake"}, nil
}
