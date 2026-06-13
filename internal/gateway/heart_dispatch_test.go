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
				reflex:  func(_ context.Context, _ heart.Event, id string) { got = "reflex"; gotReflexID = id },
				wake:    func(_ context.Context, _ heart.Event, _ attention.Verdict) { got = "wake" },
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
}
