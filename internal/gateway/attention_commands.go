package gateway

import (
	"context"
	"fmt"
	"strings"

	"github.com/Forest-Isle/daimon/internal/attention"
	"github.com/Forest-Isle/daimon/internal/channel"
)

const attentionUsage = `**Attention Commands**
- /attention events — list recent routed events (to find an event id)
- /attention recent — show recent routing corrections
- /attention feedback <event_id> <given> <expected> [note] — record a correction
  (given/expected: ignore | reflex | cognize | wake_user)

Corrections train the sleep phase to synthesize better routing rules.`

// handleAttention records and inspects attention-routing feedback — the
// correction signal the sleep phase later mines into rules. It requires the
// heart subsystem (agent.heart_enabled), which owns the feedback store.
func (gw *Gateway) handleAttention(ctx context.Context, _ channel.Channel, msg channel.InboundMessage) (string, error) {
	if gw.heart == nil || gw.heart.feedback == nil {
		return "Attention router is not active. Set `agent.heart_enabled: true` to enable it.", nil
	}

	args := strings.TrimSpace(strings.TrimPrefix(msg.Text, "/attention"))
	fields := strings.Fields(args)
	if len(fields) == 0 || fields[0] == "help" {
		return attentionUsage, nil
	}

	switch fields[0] {
	case "events":
		events, err := gw.heart.store.RecentRouted(ctx, 20)
		if err != nil {
			return "", fmt.Errorf("list routed events: %w", err)
		}
		if len(events) == 0 {
			return "No routed events yet.", nil
		}
		var b strings.Builder
		b.WriteString("**Recent routed events**\n")
		for _, ev := range events {
			fmt.Fprintf(&b, "- %s  %s/%s  (%s)\n", ev.ID, ev.Source, ev.Kind, ev.OccurredAt)
		}
		return b.String(), nil

	case "recent":
		recent, err := gw.heart.feedback.Recent(ctx, 20)
		if err != nil {
			return "", fmt.Errorf("list feedback: %w", err)
		}
		if len(recent) == 0 {
			return "No routing corrections recorded yet.", nil
		}
		var b strings.Builder
		b.WriteString("**Recent routing corrections**\n")
		for _, fb := range recent {
			fmt.Fprintf(&b, "- %s: given=%s expected=%s", fb.EventID, fb.GivenAction, fb.ExpectedAction)
			if fb.Note != "" {
				fmt.Fprintf(&b, " — %s", fb.Note)
			}
			b.WriteByte('\n')
		}
		return b.String(), nil

	case "feedback":
		// /attention feedback <event_id> <given> <expected> [note...]
		if len(fields) < 4 {
			return "Usage: /attention feedback <event_id> <given> <expected> [note]", nil
		}
		eventID := fields[1]
		given, err := attention.ParseAction(fields[2])
		if err != nil {
			return fmt.Sprintf("Unknown given action %q (use: ignore|reflex|cognize|wake_user).", fields[2]), nil
		}
		expected, err := attention.ParseAction(fields[3])
		if err != nil {
			return fmt.Sprintf("Unknown expected action %q (use: ignore|reflex|cognize|wake_user).", fields[3]), nil
		}
		note := strings.Join(fields[4:], " ")
		if err := gw.heart.feedback.Record(ctx, attention.Feedback{
			EventID:        eventID,
			GivenAction:    given.String(),
			ExpectedAction: expected.String(),
			Note:           note,
		}); err != nil {
			return "", fmt.Errorf("record feedback: %w", err)
		}
		return fmt.Sprintf("Recorded: event %s was %s, should have been %s.", eventID, given.String(), expected.String()), nil

	default:
		return attentionUsage, nil
	}
}
