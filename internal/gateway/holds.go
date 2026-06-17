package gateway

import (
	"context"
	"log/slog"
	"time"

	"github.com/Forest-Isle/daimon/internal/action"
)

const holdTimeFormat = "2006-01-02 15:04:05"

func (gw *Gateway) drainHolds(ctx context.Context) {
	if gw == nil || gw.config == nil || !gw.config.Config().Agent.Action.HoldEnabled {
		return
	}
	if gw.toolSub == nil || gw.toolSub.ActionStore == nil || gw.toolSub.Registry == nil {
		return
	}

	nowStr := time.Now().UTC().Format(holdTimeFormat)
	holds, err := gw.toolSub.ActionStore.DueHolds(ctx, nowStr)
	if err != nil {
		slog.Warn("holds: list due failed", "err", err)
		return
	}
	for _, h := range holds {
		claimed, err := gw.toolSub.ActionStore.ClaimHold(ctx, h.ID)
		if err != nil {
			slog.Warn("holds: claim failed", "id", h.ID, "err", err)
			continue
		}
		if !claimed {
			continue
		}

		// Fire the deferred action. Terminal state records the outcome so a
		// failed fire is auditable and is NOT silently marked executed; neither
		// outcome re-drains (the recall window was the contract, not delivery).
		state := "executed"
		if t, getErr := gw.toolSub.Registry.Get(h.ToolName); getErr != nil {
			slog.Warn("holds: tool unavailable", "id", h.ID, "tool", h.ToolName, "err", getErr)
			state = "failed"
		} else if res, execErr := t.Execute(ctx, []byte(h.Payload)); execErr != nil {
			slog.Warn("holds: execute failed", "id", h.ID, "tool", h.ToolName, "err", execErr)
			state = "failed"
		} else if res.Error != "" {
			slog.Warn("holds: execute reported error", "id", h.ID, "tool", h.ToolName, "tool_err", res.Error)
			state = "failed"
		}

		if err := gw.toolSub.ActionStore.MarkHoldState(ctx, h.ID, state); err != nil {
			slog.Warn("holds: mark state failed", "id", h.ID, "state", state, "err", err)
		}
		if err := gw.toolSub.ActionStore.RecordAttempt(ctx, action.Compensable, h.ToolName, false); err != nil {
			slog.Warn("holds: record trust attempt failed", "id", h.ID, "tool", h.ToolName, "err", err)
		}
	}
}

func holdDrainInterval(seconds int) time.Duration {
	if seconds <= 0 {
		seconds = 15
	}
	return time.Duration(seconds) * time.Second
}
