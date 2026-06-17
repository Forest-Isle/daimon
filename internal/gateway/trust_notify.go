package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Forest-Isle/daimon/internal/action"
	"github.com/Forest-Isle/daimon/internal/world"
)

type gatewayTrustNotifier struct {
	gw *Gateway
}

func newGatewayTrustNotifier(gw *Gateway) action.TrustNotifier {
	return gatewayTrustNotifier{gw: gw}
}

func (n gatewayTrustNotifier) TrustPromoted(ctx context.Context, class action.Class, contextKey string, from, to action.Level) {
	if n.gw == nil {
		return
	}
	bg := context.Background()
	if ctx != nil {
		bg = context.WithoutCancel(ctx)
	}
	go n.deliver(bg, class, contextKey, from, to)
}

func (n gatewayTrustNotifier) deliver(ctx context.Context, class action.Class, contextKey string, from, to action.Level) {
	gw := n.gw
	if gw == nil {
		return
	}
	text := fmt.Sprintf("Trust raised: %s actions for %q now %s (was %s). I'll act with more autonomy here. To revoke, run: daimon trust correct %s %q", class, contextKey, to, from, class, contextKey)

	notifier, target := gw.primaryNotifier()
	if notifier == nil {
		slog.Warn("trust: promotion but no notification channel", "class", class.String(), "context_key", contextKey, "from", from.String(), "to", to.String())
	} else if err := notifier.SendNotification(ctx, target, text); err != nil {
		slog.Warn("trust: promotion notify failed", "class", class.String(), "context_key", contextKey, "err", err)
	}

	if gw.toolSub == nil || gw.toolSub.WorldStore == nil {
		return
	}
	if err := gw.toolSub.WorldStore.AppendJournal(ctx, world.JournalEntry{
		Kind:       "trust_promotion",
		Summary:    text,
		OccurredAt: time.Now().UTC().Format("2006-01-02 15:04:05"),
	}); err != nil {
		slog.Warn("trust: promotion audit failed", "class", class.String(), "context_key", contextKey, "err", err)
	}
}
