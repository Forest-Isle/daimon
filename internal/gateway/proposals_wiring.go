package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/attention"
	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/proposals"
	"github.com/Forest-Isle/daimon/internal/skill"
)

// wireProposals builds the §4.9 anticipation loop's decision + delivery objects.
// The coordinator turns a user's accept/dismiss on a queued proposal into action
// (accept fires the action plan as an autonomous episode; dismiss records an
// anticipation-quality training signal). The deliverer pushes undelivered
// proposals to the proposal-capable channel, resolved lazily at send time since
// channels are registered after Wire(). Both reuse the shared proposals store and
// sleep clock.
func (gw *Gateway) wireProposals(store *proposals.Store, now func() int64) {
	gw.proposals = &proposalCoordinator{
		store: store,
		fire: func(ctx context.Context, idem, goal, trigger, class string) error {
			_, err := gw.agent.RunInternalEpisode(ctx, idem, goal, trigger, class)
			return err
		},
		promote: func(ctx context.Context, slug string) error {
			_, err := skill.PromoteDraft(appdir.SkillsStagingDir(), appdir.SkillsDir(), slug)
			return err
		},
		feedback: attention.NewFeedbackStore(gw.db.DB).Record,
		now:      now,
	}
	gw.proposalDeliverer = &proposalDeliverer{
		store: store,
		send:  gw.sendProposal,
		now:   now,
	}
}

// proposalChannel resolves the channel that delivers proposals (Telegram for a
// single-user sovereign agent), addressed to the principal. Mirrors
// primaryNotifier. Returns nil when no proposal-capable channel is registered.
func (gw *Gateway) proposalChannel() (channel.ProposalSender, channel.MessageTarget) {
	if ch, ok := gw.channels.Channels()["telegram"]; ok {
		if ps, ok := ch.(channel.ProposalSender); ok {
			target := channel.MessageTarget{Channel: "telegram"}
			if ids := gw.Config().Telegram.AllowedUserIDs; len(ids) > 0 {
				target.ChannelID = strconv.FormatInt(ids[0], 10)
			}
			return ps, target
		}
	}
	return nil, channel.MessageTarget{}
}

// sendProposal is the deliverer's send closure: push one proposal to the
// proposal-capable channel. Returns an error (leaving the proposal undelivered
// for next-cycle retry) when no such channel is available.
func (gw *Gateway) sendProposal(ctx context.Context, p proposals.Proposal) error {
	ps, target := gw.proposalChannel()
	if ps == nil {
		return fmt.Errorf("no proposal-capable channel registered")
	}
	return ps.SendProposal(ctx, target, p.ID, p.Title, p.Body)
}

// deliverProposals pushes any undelivered pending proposals to the user. Called
// after a sleep cycle (which is what creates proposals). Best-effort: a failure
// is logged, and undelivered proposals are retried on the next cycle.
func (gw *Gateway) deliverProposals(ctx context.Context) {
	if gw.proposalDeliverer == nil {
		return
	}
	if err := gw.proposalDeliverer.deliverPending(ctx); err != nil {
		slog.Warn("proposals: delivery cycle failed", "err", err)
	}
}

// registerProposalHandler wires the proposal-capable channel's inline buttons to
// the coordinator. Called at Start, after channels are registered. A tap's
// decision is fire-and-forget (the adapter invokes the handler in a goroutine);
// errors are logged, not surfaced to the user mid-tap.
func (gw *Gateway) registerProposalHandler() {
	if gw.proposals == nil {
		return
	}
	ps, _ := gw.proposalChannel()
	if ps == nil {
		return
	}
	ps.SetProposalHandler(func(ctx context.Context, id string, accept bool) {
		var err error
		if accept {
			err = gw.proposals.Accept(ctx, id)
		} else {
			err = gw.proposals.Dismiss(ctx, id)
		}
		if err != nil {
			slog.Warn("proposals: decision failed", "id", id, "accept", accept, "err", err)
		}
	})
}
