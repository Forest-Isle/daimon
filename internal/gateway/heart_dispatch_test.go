package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/Forest-Isle/daimon/internal/attention"
	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/heart"
)

// TestEventDispatcherRoutesVerdicts verifies each attention verdict reaches the
// right branch, and that a routing error falls through to Cognize (prefer to
// wake over dropping a possibly-important event).
func TestEventDispatcherRoutesVerdicts(t *testing.T) {
	cases := []struct {
		name     string
		verdict  attention.Verdict
		routeErr error
		want     string // expected branch ("" = none, i.e. ignore)
	}{
		{"ignore", attention.Verdict{Action: attention.Ignore}, nil, ""},
		{"cognize", attention.Verdict{Action: attention.Cognize}, nil, "cognize"},
		{"reflex", attention.Verdict{Action: attention.Reflex, ReflexID: "daily_report"}, nil, "reflex"},
		{"wake", attention.Verdict{Action: attention.WakeUser}, nil, "wake"},
		{"route_error_defaults_cognize", attention.Verdict{Action: attention.Ignore}, errors.New("boom"), "cognize"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got, gotReflexID string
			d := &eventDispatcher{
				route: func(_ context.Context, _ heart.Event) (attention.Verdict, error) {
					return tc.verdict, tc.routeErr
				},
				cognize: func(_ context.Context, _ heart.Event) { got = "cognize" },
				reflex: func(_ context.Context, _ heart.Event, id string) error {
					got = "reflex"
					gotReflexID = id
					return nil
				},
				wake: func(_ context.Context, _ heart.Event, _ attention.Verdict) { got = "wake" },
			}
			d.handle(context.Background(), heart.Event{Source: "timer", Kind: "internal.heartbeat"})
			if got != tc.want {
				t.Fatalf("verdict %s: want branch %q, got %q", tc.name, tc.want, got)
			}
			if tc.want == "reflex" && gotReflexID != "daily_report" {
				t.Fatalf("reflex id not propagated: %q", gotReflexID)
			}
		})
	}
}

// TestEventDispatcherIgnoresChatMessages verifies a "message" event (chat
// ingress, handled synchronously elsewhere) is never routed or cognized by the
// dispatcher — guarding against a stray/recovered chat event becoming a spurious
// autonomous episode.
func TestEventDispatcherIgnoresChatMessages(t *testing.T) {
	routed := false
	cognized := false
	d := &eventDispatcher{
		route: func(_ context.Context, _ heart.Event) (attention.Verdict, error) {
			routed = true
			return attention.Verdict{Action: attention.Cognize}, nil
		},
		cognize: func(_ context.Context, _ heart.Event) { cognized = true },
		reflex:  func(_ context.Context, _ heart.Event, _ string) error { return nil },
		wake:    func(_ context.Context, _ heart.Event, _ attention.Verdict) {},
	}
	d.handle(context.Background(), heart.Event{Source: "telegram", Kind: "message", Payload: "hi"})
	if routed || cognized {
		t.Fatalf("message event must not be routed/cognized (routed=%v cognized=%v)", routed, cognized)
	}
}

// TestEventDispatcherDailyBriefBypassesCognition verifies the deterministic
// daily brief timer is delivered off the routing/cognition path.
func TestEventDispatcherDailyBriefBypassesCognition(t *testing.T) {
	routed := false
	cognized := false
	reflexed := false
	woke := false
	briefed := false
	d := &eventDispatcher{
		route: func(_ context.Context, _ heart.Event) (attention.Verdict, error) {
			routed = true
			return attention.Verdict{Action: attention.Cognize}, nil
		},
		cognize: func(_ context.Context, _ heart.Event) { cognized = true },
		reflex:  func(_ context.Context, _ heart.Event, _ string) error { reflexed = true; return nil },
		wake:    func(_ context.Context, _ heart.Event, _ attention.Verdict) { woke = true },
		brief:   func(_ context.Context) { briefed = true },
	}
	d.handle(context.Background(), heart.Event{Source: "timer", Kind: "internal.daily_brief"})
	if !briefed {
		t.Fatal("daily brief event must call brief closure")
	}
	if routed || cognized || reflexed || woke {
		t.Fatalf("daily brief must bypass routing/cognition (routed=%v cognized=%v reflexed=%v woke=%v)", routed, cognized, reflexed, woke)
	}

	routed = false
	d = &eventDispatcher{
		route: func(_ context.Context, _ heart.Event) (attention.Verdict, error) {
			routed = true
			return attention.Verdict{Action: attention.Cognize}, nil
		},
		brief: nil,
	}
	d.handle(context.Background(), heart.Event{Source: "timer", Kind: "internal.daily_brief"})
	if routed {
		t.Fatal("daily brief event with nil brief closure must not route")
	}
}

// TestEventDispatcherHealthBypassesCognition verifies the deterministic
// selfops health timer is handled off the routing/cognition path.
func TestEventDispatcherHealthBypassesCognition(t *testing.T) {
	routed := false
	cognized := false
	briefed := false
	healthChecked := false
	d := &eventDispatcher{
		route: func(_ context.Context, _ heart.Event) (attention.Verdict, error) {
			routed = true
			return attention.Verdict{Action: attention.Cognize}, nil
		},
		cognize: func(_ context.Context, _ heart.Event) { cognized = true },
		brief:   func(_ context.Context) { briefed = true },
		health:  func(_ context.Context) { healthChecked = true },
	}
	d.handle(context.Background(), heart.Event{Source: "timer", Kind: "internal.health"})
	if !healthChecked {
		t.Fatal("health event must call health closure")
	}
	if routed || cognized || briefed {
		t.Fatalf("health must bypass routing/cognition (routed=%v cognized=%v briefed=%v)", routed, cognized, briefed)
	}

	routed = false
	d = &eventDispatcher{
		route: func(_ context.Context, _ heart.Event) (attention.Verdict, error) {
			routed = true
			return attention.Verdict{Action: attention.Cognize}, nil
		},
		health: nil,
	}
	d.handle(context.Background(), heart.Event{Source: "timer", Kind: "internal.health"})
	if routed {
		t.Fatal("health event with nil health closure must not route")
	}
}

// TestEventDispatcherSleepBypassesCognition verifies the autonomous sleep timer
// is handled off the routing/cognition path.
func TestEventDispatcherSleepBypassesCognition(t *testing.T) {
	routed := false
	cognized := false
	slept := false
	d := &eventDispatcher{
		route: func(_ context.Context, _ heart.Event) (attention.Verdict, error) {
			routed = true
			return attention.Verdict{Action: attention.Cognize}, nil
		},
		cognize: func(_ context.Context, _ heart.Event) { cognized = true },
		sleep:   func(_ context.Context) { slept = true },
	}
	d.handle(context.Background(), heart.Event{Source: "timer", Kind: "internal.sleep"})
	if !slept {
		t.Fatal("sleep event must call sleep closure")
	}
	if routed || cognized {
		t.Fatalf("sleep must bypass routing/cognition (routed=%v cognized=%v)", routed, cognized)
	}

	routed = false
	d = &eventDispatcher{
		route: func(_ context.Context, _ heart.Event) (attention.Verdict, error) {
			routed = true
			return attention.Verdict{Action: attention.Cognize}, nil
		},
		sleep: nil,
	}
	d.handle(context.Background(), heart.Event{Source: "timer", Kind: "internal.sleep"})
	if routed {
		t.Fatal("sleep event with nil sleep closure must not route")
	}
}

func TestEventDispatcherRecordsOnlyRealActivity(t *testing.T) {
	activity := 0
	d := &eventDispatcher{
		route: func(_ context.Context, _ heart.Event) (attention.Verdict, error) {
			return attention.Verdict{Action: attention.Ignore}, nil
		},
		recordActivity: func() { activity++ },
	}

	d.handle(context.Background(), heart.Event{Source: "timer", Kind: "internal.heartbeat"})
	if activity != 0 {
		t.Fatalf("internal event must not count as activity, got %d", activity)
	}

	d.handle(context.Background(), heart.Event{Source: "mail", Kind: "mail.received"})
	if activity != 1 {
		t.Fatalf("real event must count as activity, got %d", activity)
	}
}

// TestHandleApprovalDeniesNilChannel pins the autonomous-episode safety rule:
// with no interactive channel there is no one to sign off, so approval-required
// tools must be denied (not auto-approved).
func TestHandleApprovalDeniesNilChannel(t *testing.T) {
	gw := &Gateway{}
	ok, err := gw.handleApproval(context.Background(), nil, channel.MessageTarget{}, "bash", "rm -rf /")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("nil channel must deny approval-required tools, got approved")
	}
}

// TestGoalForEvent verifies the heartbeat gets its review goal and other kinds a
// generic one.
func TestGoalForEvent(t *testing.T) {
	if g := goalForEvent(heart.Event{Kind: "internal.heartbeat"}); g == "" || g == "Handle internal event: internal.heartbeat" {
		t.Fatalf("heartbeat goal should be the review goal, got %q", g)
	}
	if g := goalForEvent(heart.Event{Kind: "mail.received"}); g != "Handle internal event: mail.received" {
		t.Fatalf("generic goal wrong: %q", g)
	}
	// A planted follow-up carries its re-entry goal in the payload.
	if g := goalForEvent(heart.Event{Kind: "internal.followup", Payload: "Resume the deploy"}); g != "Resume the deploy" {
		t.Fatalf("followup goal should be the payload, got %q", g)
	}
	// A follow-up with no goal falls back to a generic continuation.
	if g := goalForEvent(heart.Event{Kind: "internal.followup"}); g == "" || g != "Continue the work that planted this follow-up." {
		t.Fatalf("empty-payload followup goal wrong: %q", g)
	}
}
