package gateway

import (
	"context"
	"log/slog"
	"strings"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/taskruntime"
)

func (gw *Gateway) saveTaskCheckpoint(ctx context.Context, msg channel.InboundMessage) string {
	if gw.taskLedger == nil || gw.sessions == nil {
		return ""
	}
	sess, err := gw.sessions.Get(ctx, msg.Channel, msg.ChannelID)
	if err != nil {
		slog.Warn("gateway: failed to load session for task checkpoint", "channel", msg.Channel, "channel_id", msg.ChannelID, "err", err)
		return ""
	}
	if sess == nil {
		return ""
	}

	history := sess.History()
	plan := strings.TrimSpace(sess.GetMetadata("plan"))
	if plan == "" {
		plan = "{}"
	}
	if err := gw.taskLedger.SaveCheckpoint(ctx, taskruntime.Checkpoint{
		SessionID:    sess.ID,
		SubtaskIndex: len(history),
		Observations: recentSessionObservations(history, 5),
		PlanJSON:     plan,
	}); err != nil {
		slog.Warn("gateway: failed to save task checkpoint", "session", sess.ID, "err", err)
	}

	if msg.Channel == "scheduler" {
		if err := gw.taskLedger.MarkRunning(ctx, taskruntime.ScheduledLedgerID(msg.ChannelID), taskruntime.Metadata{
			SessionID:        sess.ID,
			SessionChannel:   msg.Channel,
			SessionChannelID: msg.ChannelID,
		}, "checkpoint saved for session "+sess.ID); err != nil {
			slog.Warn("gateway: failed to attach scheduler session metadata", "task", msg.ChannelID, "session", sess.ID, "err", err)
		}
	}
	return latestTaskResult(history)
}

func recentSessionObservations(history []session.Message, max int) []string {
	if max <= 0 {
		max = 5
	}
	out := make([]string, 0, max)
	for i := len(history) - 1; i >= 0 && len(out) < max; i-- {
		switch history[i].Role {
		case "assistant", "tool_result":
		default:
			continue
		}
		line := compactObservation(history[i].Content)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func latestTaskResult(history []session.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "assistant" {
			continue
		}
		if line := compactObservation(history[i].Content); line != "" {
			return line
		}
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "tool_result" {
			continue
		}
		if line := compactObservation(history[i].Content); line != "" {
			return line
		}
	}
	return ""
}

func compactObservation(s string) string {
	return compactLine(s, 240)
}
