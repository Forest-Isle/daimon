package gateway

import (
	"context"
	"fmt"
	"strings"

	"github.com/Forest-Isle/daimon/internal/attention"
	"github.com/Forest-Isle/daimon/internal/proposals"
)

// proposalCoordinator closes the §4.9 anticipation loop: it turns a user's
// decision on a queued proposal into action. Accept fires the proposal's action
// plan as an autonomous episode; Dismiss records a training signal so the
// anticipation engine learns the proposal was unwanted. It is dependency-injected
// (closures, not the whole gateway) so each branch is unit-testable in isolation.
type proposalCoordinator struct {
	store *proposals.Store
	// fire runs an accepted proposal's action plan as an autonomous episode. The
	// proposal id is the idempotency key so a re-delivered or double-tapped accept
	// cannot fire the plan twice.
	fire func(ctx context.Context, idempotencyKey, goal, trigger, activityClass string) error
	// promote deterministically moves a staged distilled skill draft into the
	// active skills directory. It is intentionally not an episode/LLM path.
	promote func(ctx context.Context, slug string) error
	// feedback records anticipation-quality corrections (a dismissed proposal).
	feedback func(ctx context.Context, fb attention.Feedback) error
	now      func() int64
}

// loadLive fetches a proposal and rejects one past its expiry window. A delivered
// inline button outlives the proposal in the user's chat history; tapping a stale
// button must not act on a dead proposal. Mirrors ListPending's live predicate
// (expires_at 0 = no expiry) so the decision UX and the queue agree on liveness.
func (c *proposalCoordinator) loadLive(ctx context.Context, id string) (proposals.Proposal, error) {
	p, err := c.store.Get(ctx, id)
	if err != nil {
		return proposals.Proposal{}, err
	}
	if p.ExpiresAt != 0 && p.ExpiresAt <= c.now() {
		return proposals.Proposal{}, fmt.Errorf("proposal %q has expired", id)
	}
	return p, nil
}

// Accept marks a live proposal accepted, then runs its typed action. The action
// kind is fail-closed: unknown kinds error before Decide and run nothing. Promote
// proposals validate their ref/handler before Decide, so bad rows remain pending
// and fixable instead of being consumed. Once validation passes, Decide updates
// only a still-pending row, so a second accept (double-tap, redelivery race,
// concurrent callbacks) fails before any action runs — accept is exactly-once. An
// episode proposal with no action plan is recorded as accepted but fires nothing
// (purely informational). Empty historical action_kind rows still use episode.
//
// Action-failure window: if Decide succeeds but fire/promote returns an error,
// the proposal is left accepted yet no action ran, and the same id can no longer
// be retried through this path (state != pending). This is deliberately not an
// outbox. Episode loss self-heals because an accepted title no longer blocks
// PendingTitles, so a still-due commitment can be re-proposed. Promote loss
// self-heals because the draft remains in staging and the accepted proposal no
// longer blocks PendingPromoteRefs, so distill-screen can re-propose it. The
// caller logs the error so the failure is visible, not silent.
func (c *proposalCoordinator) Accept(ctx context.Context, id string) error {
	p, err := c.loadLive(ctx, id)
	if err != nil {
		return err
	}
	switch p.ActionKind {
	case proposals.ActionKindPromoteSkill:
		ref := strings.TrimSpace(p.ActionRef)
		if ref == "" {
			return fmt.Errorf("promote proposal %q has no action_ref", id)
		}
		if c.promote == nil {
			return fmt.Errorf("promote proposal %q has no promote handler", id)
		}
		if err := c.store.Decide(ctx, id, proposals.StateAccepted, c.now()); err != nil {
			return err
		}
		return c.promote(ctx, ref)
	case proposals.ActionKindEpisode, "":
		if err := c.store.Decide(ctx, id, proposals.StateAccepted, c.now()); err != nil {
			return err
		}
		goal := strings.TrimSpace(p.ActionPlan)
		if goal == "" {
			return nil
		}
		return c.fire(ctx, id, goal, p.Body, "proposal")
	default:
		return fmt.Errorf("proposal %q has unknown action_kind %q", id, p.ActionKind)
	}
}

// Dismiss marks a pending proposal dismissed and records it as a training signal
// for the anticipation engine: the agent proposed something the user did not
// want. The dismissal gates the same way Accept does (one terminal decision per
// proposal). The dismissal persists even if the feedback write fails (the signal
// is best-effort); the error is surfaced for the caller to log.
func (c *proposalCoordinator) Dismiss(ctx context.Context, id string) error {
	p, err := c.loadLive(ctx, id)
	if err != nil {
		return err
	}
	if err := c.store.Decide(ctx, id, proposals.StateDismissed, c.now()); err != nil {
		return err
	}
	return c.feedback(ctx, attention.Feedback{
		EventID:        id,
		ExpectedAction: "ignore",
		GivenAction:    "proposed",
		Note:           p.Title,
	})
}
