package gateway

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/daimon/internal/attention"
	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/proposals"
	"github.com/Forest-Isle/daimon/internal/store"
)

// fakeProposalChannel implements channel.Channel + channel.ProposalSender so the
// delivery/decision wiring can be exercised without a live Telegram bot.
type fakeProposalChannel struct {
	sent    []string
	handler func(ctx context.Context, id string, accept bool)
}

func (f *fakeProposalChannel) Name() string                                        { return "telegram" }
func (f *fakeProposalChannel) Start(context.Context, channel.InboundHandler) error { return nil }
func (f *fakeProposalChannel) Send(context.Context, channel.OutboundMessage) error { return nil }
func (f *fakeProposalChannel) SendStreaming(context.Context, channel.MessageTarget) (channel.StreamUpdater, error) {
	return nil, nil
}
func (f *fakeProposalChannel) Stop(context.Context) error { return nil }
func (f *fakeProposalChannel) SendProposal(_ context.Context, _ channel.MessageTarget, id, _, _ string) error {
	f.sent = append(f.sent, id)
	return nil
}
func (f *fakeProposalChannel) SetProposalHandler(h func(ctx context.Context, id string, accept bool)) {
	f.handler = h
}

// TestProposalsDeliverAndDecideWiring proves the slice-3 glue end to end: a queued
// proposal is delivered through the proposal-capable channel and marked delivered;
// the registered handler routes a tap to the coordinator, firing the action plan.
func TestProposalsDeliverAndDecideWiring(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "wiring.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ps := proposals.NewStore(db.DB)
	ctx := context.Background()
	if err := ps.Create(ctx, proposals.Proposal{ID: "p1", Title: "prep", ActionPlan: "go", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}

	fake := &fakeProposalChannel{}
	var fired []string
	gw := &Gateway{
		channels: &ChannelSubsystem{channels: map[string]channel.Channel{"telegram": fake}},
		config:   &ConfigSubsystem{cfg: &config.Config{}},
		proposals: &proposalCoordinator{
			store: ps,
			fire: func(_ context.Context, idem, goal, _, class string) error {
				fired = append(fired, idem+"|"+goal+"|"+class)
				return nil
			},
			feedback: func(context.Context, attention.Feedback) error { return nil },
			now:      func() int64 { return 1000 },
		},
	}
	gw.proposalDeliverer = &proposalDeliverer{store: ps, send: gw.sendProposal, now: func() int64 { return 1000 }}

	// Delivery pushes the proposal through the channel and marks it delivered.
	gw.deliverProposals(ctx)
	if len(fake.sent) != 1 || fake.sent[0] != "p1" {
		t.Fatalf("expected p1 delivered once, got %v", fake.sent)
	}
	if undel, _ := ps.ListUndelivered(ctx, 2000); len(undel) != 0 {
		t.Fatalf("delivered proposal must be marked, got %v", undel)
	}

	// A second cycle delivers nothing more.
	gw.deliverProposals(ctx)
	if len(fake.sent) != 1 {
		t.Fatalf("delivered proposal must not be re-pushed, got %v", fake.sent)
	}

	// Registration wires the channel's inline buttons to the coordinator.
	gw.registerProposalHandler()
	if fake.handler == nil {
		t.Fatal("registerProposalHandler did not set the channel handler")
	}

	// A tap routes to the coordinator: accept fires the action plan as an episode.
	fake.handler(ctx, "p1", true)
	if len(fired) != 1 || fired[0] != "p1|go|proposal" {
		t.Fatalf("accept tap must fire the action plan, got %v", fired)
	}
}

// TestSendProposalNoChannel asserts delivery degrades safely when no
// proposal-capable channel is registered (e.g. Telegram disabled): the send
// errors so the deliverer leaves the proposal undelivered for a later cycle.
func TestSendProposalNoChannel(t *testing.T) {
	gw := &Gateway{
		channels: &ChannelSubsystem{channels: map[string]channel.Channel{}},
		config:   &ConfigSubsystem{cfg: &config.Config{}},
	}
	if err := gw.sendProposal(context.Background(), proposals.Proposal{ID: "p1"}); err == nil {
		t.Fatal("sendProposal must error when no proposal-capable channel exists")
	}
}
