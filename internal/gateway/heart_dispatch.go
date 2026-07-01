package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Forest-Isle/daimon/internal/attention"
	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/episode"
	"github.com/Forest-Isle/daimon/internal/heart"
)

// eventDispatcher turns a routed heart event into an action by consulting the
// attention router and fanning out to the matching handler. It is deliberately
// small and dependency-injected (closures, not the whole gateway) so each branch
// is unit-testable in isolation.
type eventDispatcher struct {
	route          func(ctx context.Context, ev heart.Event) (attention.Verdict, error)
	cognize        func(ctx context.Context, ev heart.Event)
	reflex         func(ctx context.Context, ev heart.Event, reflexID string) error
	wake           func(ctx context.Context, ev heart.Event, v attention.Verdict)
	brief          func(ctx context.Context)
	health         func(ctx context.Context)
	sleep          func(ctx context.Context)
	recordActivity func()
}

// handle is the heart.Handler: route the event, then dispatch on the verdict. A
// routing error is not fatal — we fall through to Cognize (prefer to wake rather
// than silently drop a possibly-important event), matching attention's bias.
func (d *eventDispatcher) handle(ctx context.Context, ev heart.Event) string {
	// Chat "message" events are recorded for audit/dedup and handled synchronously
	// by the chat path (HandleMessage); they are never dispatched here. Ignore one
	// defensively in case a stray/recovered message event reaches the dispatcher,
	// so it is never re-handled as an autonomous episode.
	if ev.Kind == "message" {
		slog.Debug("heart: ignoring chat message event in dispatcher", "id", ev.ID, "source", ev.Source)
		return "skipped"
	}
	// The daily-brief timer is handled deterministically off the cognition
	// path (constitution #5/#7): assemble + deliver the digest, never route
	// to the model. A nil brief closure (heart wired without a deliverer)
	// is a no-op.
	if ev.Kind == "internal.daily_brief" {
		if d.brief != nil {
			d.brief(ctx)
		}
		return "brief"
	}
	// The selfops health timer is deterministic and stays off the cognition
	// path (constitution #5/#7): inspect cheap signals, notify/write proposals,
	// never route to the model.
	if ev.Kind == "internal.health" {
		if d.health != nil {
			d.health(ctx)
		}
		return "health"
	}
	// The autonomous sleep timer runs fixed maintenance jobs directly, off the
	// routing/cognition path. It starts the cycle in a separate goroutine.
	if ev.Kind == "internal.sleep" {
		if d.sleep != nil {
			d.sleep(ctx)
		}
		return "sleep"
	}
	if !strings.HasPrefix(ev.Kind, "internal.") && d.recordActivity != nil {
		d.recordActivity()
	}
	v, err := d.route(ctx, ev)
	if err != nil {
		slog.Error("heart: route failed; defaulting to cognize", "kind", ev.Kind, "err", err)
		v = attention.Verdict{Action: attention.Cognize, Reason: "route error: prefer to wake"}
	}
	switch v.Action {
	case attention.Ignore:
		slog.Debug("heart: ignore", "source", ev.Source, "kind", ev.Kind, "reason", v.Reason)
	case attention.Reflex:
		if d.reflex == nil {
			slog.Error("heart: reflex requested but executor is not wired", "kind", ev.Kind, "reflex_id", v.ReflexID)
			break
		}
		if err := d.reflex(ctx, ev, v.ReflexID); err != nil {
			slog.Error("heart: reflex failed", "kind", ev.Kind, "reflex_id", v.ReflexID, "err", err)
		}
	case attention.WakeUser:
		d.wake(ctx, ev, v)
	default: // Cognize — the deliberate default for any unclassified event.
		d.cognize(ctx, ev)
	}
	return v.Action.String()
}

// newEventDispatcher wires the dispatcher branches to the live gateway: Cognize
// fires an autonomous (channel-less) episode, WakeUser notifies the primary
// channel, Reflex executes an explicitly configured deterministic workflow.
func (gw *Gateway) newEventDispatcher() *eventDispatcher {
	return &eventDispatcher{
		route: gw.heart.chain.Route,
		cognize: func(ctx context.Context, ev heart.Event) {
			// Throttle enforcement only gates autonomous Cognize episodes. WakeUser,
			// Reflex, deterministic branches, and chat are structurally unaffected;
			// the gate itself is config-gated, reversible on refresh, and user-overridable.
			if gw.throttle != nil && gw.throttle.ShouldSkip(ev.Kind) {
				slog.Info("heart: throttled class skipped autonomous episode", "kind", ev.Kind)
				return
			}
			// Pass the event id as the idempotency key: heart's at-least-once replay
			// (after a crash before the event was marked routed) re-delivers the same
			// event id, and the kernel skips an already-completed episode.
			// The event kind is the activity class for cost accounting (§4.11):
			// autonomous episodes group by what triggered them (heartbeat, followup, …).
			if _, err := gw.agent.RunInternalEpisode(ctx, ev.ID, goalForEvent(ev), ev.Payload, ev.Kind); err != nil {
				slog.Error("heart: internal episode failed", "kind", ev.Kind, "err", err)
			}
		},
		reflex: func(ctx context.Context, ev heart.Event, reflexID string) error {
			if gw.reflexes == nil {
				return fmt.Errorf("reflex executor unavailable")
			}
			run, err := gw.reflexes.Execute(ctx, reflexID, ev)
			if err != nil {
				return err
			}
			slog.Info("heart: reflex executed", "kind", ev.Kind, "reflex_id", reflexID, "workflow", run.WorkflowName, "status", run.Status)
			return nil
		},
		wake:   gw.wakeUser,
		brief:  gw.deliverDailyBrief,
		health: gw.runHealthCheck,
		sleep:  gw.triggerAutonomousSleep,
		recordActivity: func() {
			gw.lastEventAt.Store(time.Now().Unix())
		},
	}
}

// goalForEvent maps an event to the episode goal that should handle it.
func goalForEvent(ev heart.Event) string {
	switch ev.Kind {
	case "internal.heartbeat":
		return "Review active commitments and surface anything that needs attention; if nothing is due, close quietly without taking action."
	case "internal.followup":
		// A planted follow-up carries its re-entry goal in the payload.
		if goal := strings.TrimSpace(ev.Payload); goal != "" {
			return goal
		}
		return "Continue the work that planted this follow-up."
	default:
		return "Handle internal event: " + ev.Kind
	}
}

// followUpPlanter adapts the heart follow-up store to episode.FollowUpPlanter: it
// schedules a timer follow-up by computing its fire time from the follow-up's
// Detail (a Go duration string, defaulting to 1h).
type followUpPlanter struct {
	store *heart.FollowUpStore
	now   func() time.Time
}

func (p followUpPlanter) Plant(ctx context.Context, episodeID string, f episode.FollowUp) error {
	now := p.now
	if now == nil {
		now = time.Now
	}
	dur := time.Hour
	if d, err := time.ParseDuration(strings.TrimSpace(f.Detail)); err == nil && d > 0 {
		dur = d
	}
	// Leave the goal empty when the model gave none: goalForEvent then supplies a
	// generic continuation goal. Do not fall back to Detail — that is the timer
	// interval, not a goal.
	return p.store.Create(ctx, heart.FollowUp{
		SourceEpisode: episodeID,
		Kind:          "timer",
		Goal:          strings.TrimSpace(f.Goal),
		Trigger:       f.Detail,
		FireAt:        now().Add(dur).Unix(),
	})
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
