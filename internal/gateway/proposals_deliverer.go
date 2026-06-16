package gateway

import (
	"context"
	"log/slog"
	"sync"

	"github.com/Forest-Isle/daimon/internal/proposals"
)

// proposalDeliverer pushes undelivered pending proposals to the user exactly the
// once. It is dependency-injected (store + sender + clock) so its delivery
// semantics — send-then-mark, skip on send failure — are unit-testable in
// isolation, mirroring eventDispatcher.
type proposalDeliverer struct {
	store *proposals.Store
	send  func(ctx context.Context, p proposals.Proposal) error
	now   func() int64
	// mu serializes delivery cycles within the process. ListUndelivered then
	// send-then-MarkDelivered is a read-then-act window: two cycles racing (the
	// sleep-completion trigger and a timer firing together) would both read the
	// same undelivered rows and double-send before either marks. Daimon is a single
	// daemon over one SQLite file, so a process-level lock is sufficient — no
	// cross-process claim protocol needed.
	mu sync.Mutex
}

// deliverPending sends each undelivered pending proposal, then marks it delivered.
// Order matters: a proposal is marked only AFTER a successful send, so a send
// failure leaves it undelivered to be retried next cycle (at-least-once). The
// mirror risk — sent but the mark fails — re-pushes the proposal next cycle (a
// duplicate message, never a lost one); that is the deliberate bias. One proposal
// failing does not abort the batch. Cycles are serialized by mu so concurrent
// triggers cannot double-send.
func (d *proposalDeliverer) deliverPending(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	items, err := d.store.ListUndelivered(ctx, now)
	if err != nil {
		return err
	}
	for _, p := range items {
		if err := d.send(ctx, p); err != nil {
			slog.Warn("proposals: deliver failed; will retry next cycle", "id", p.ID, "err", err)
			continue
		}
		if err := d.store.MarkDelivered(ctx, p.ID, now); err != nil {
			slog.Warn("proposals: delivered but mark failed; may re-deliver", "id", p.ID, "err", err)
		}
	}
	return nil
}
