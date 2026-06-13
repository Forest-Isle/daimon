package gateway

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/Forest-Isle/daimon/internal/attention"
	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/heart"
)

// eventDispatcher turns a routed heart event into an action by consulting the
// attention router and fanning out to the matching handler. It is deliberately
// small and dependency-injected (closures, not the whole gateway) so each branch
// is unit-testable in isolation.
type eventDispatcher struct {
	route   func(ctx context.Context, ev heart.Event) (attention.Verdict, error)
	cognize func(ctx context.Context, ev heart.Event)
	reflex  func(ctx context.Context, ev heart.Event, reflexID string)
	wake    func(ctx context.Context, ev heart.Event, v attention.Verdict)
}

// handle is the heart.Handler: route the event, then dispatch on the verdict. A
// routing error is not fatal — we fall through to Cognize (prefer to wake rather
// than silently drop a possibly-important event), matching attention's bias.
func (d *eventDispatcher) handle(ctx context.Context, ev heart.Event) {
	v, err := d.route(ctx, ev)
	if err != nil {
		slog.Error("heart: route failed; defaulting to cognize", "kind", ev.Kind, "err", err)
		v = attention.Verdict{Action: attention.Cognize, Reason: "route error: prefer to wake"}
	}
	switch v.Action {
	case attention.Ignore:
		slog.Debug("heart: ignore", "source", ev.Source, "kind", ev.Kind, "reason", v.Reason)
	case attention.Reflex:
		d.reflex(ctx, ev, v.ReflexID)
	case attention.WakeUser:
		d.wake(ctx, ev, v)
	default: // Cognize — the deliberate default for any unclassified event.
		d.cognize(ctx, ev)
	}
}

// newEventDispatcher wires the dispatcher branches to the live gateway: Cognize
// fires an autonomous (channel-less) episode, WakeUser notifies the primary
// channel, Reflex is logged (workflow/skill dispatch is a later increment).
func (gw *Gateway) newEventDispatcher() *eventDispatcher {
	return &eventDispatcher{
		route: gw.heart.chain.Route,
		cognize: func(ctx context.Context, ev heart.Event) {
			if _, err := gw.agent.RunInternalEpisode(ctx, goalForEvent(ev), ev.Payload); err != nil {
				slog.Error("heart: internal episode failed", "kind", ev.Kind, "err", err)
			}
		},
		reflex: func(_ context.Context, ev heart.Event, reflexID string) {
			slog.Info("heart: reflex (stub)", "kind", ev.Kind, "reflex_id", reflexID)
		},
		wake: gw.wakeUser,
	}
}

// goalForEvent maps an event to the episode goal that should handle it.
func goalForEvent(ev heart.Event) string {
	switch ev.Kind {
	case "internal.heartbeat":
		return "Review active commitments and surface anything that needs attention; if nothing is due, close quietly without taking action."
	default:
		return "Handle internal event: " + ev.Kind
	}
}

// wakeUser delivers an urgent event to the user via the primary notification
// channel (Telegram preferred). If no channel can notify, it is logged.
func (gw *Gateway) wakeUser(ctx context.Context, ev heart.Event, v attention.Verdict) {
	notifier, target := gw.primaryNotifier()
	if notifier == nil {
		slog.Warn("heart: wake_user but no notification channel", "kind", ev.Kind, "reason", v.Reason)
		return
	}
	text := "⚠️ " + ev.Kind
	if v.Reason != "" {
		text += ": " + v.Reason
	}
	if ev.Payload != "" {
		text += "\n" + ev.Payload
	}
	if err := notifier.SendNotification(ctx, target, text); err != nil {
		slog.Warn("heart: wake_user notify failed", "kind", ev.Kind, "err", err)
	}
}

// primaryNotifier returns a channel that can deliver notifications to the user,
// preferring Telegram addressed to the first allowed user (for a single-user
// sovereign agent, that is the principal). Falls back to any notification-capable
// channel with a best-effort target.
func (gw *Gateway) primaryNotifier() (channel.NotificationSender, channel.MessageTarget) {
	chans := gw.channels.Channels()
	if ch, ok := chans["telegram"]; ok {
		if ns, ok := ch.(channel.NotificationSender); ok {
			target := channel.MessageTarget{Channel: "telegram"}
			if ids := gw.Config().Telegram.AllowedUserIDs; len(ids) > 0 {
				target.ChannelID = strconv.FormatInt(ids[0], 10)
			}
			return ns, target
		}
	}
	for name, ch := range chans {
		if ns, ok := ch.(channel.NotificationSender); ok {
			return ns, channel.MessageTarget{Channel: name}
		}
	}
	return nil, channel.MessageTarget{}
}
