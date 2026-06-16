package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Forest-Isle/daimon/internal/action"
	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/proposals"
	"github.com/Forest-Isle/daimon/internal/world"
)

const dailyBriefEntryLimit = 20
const dailyBriefBodyLimit = 140

func buildDailyBrief(now time.Time, entries []world.JournalEntry, props []proposals.Proposal, holds []action.Hold) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**每日早报** — %s  (过去 24h)\n\n", now.Format("2006-01-02 15:04"))

	b.WriteString("## 过去 24h 活动\n")
	if len(entries) == 0 {
		b.WriteString("（无活动记录）\n")
	} else {
		limit := len(entries)
		if limit > dailyBriefEntryLimit {
			limit = dailyBriefEntryLimit
		}
		for _, entry := range entries[:limit] {
			summary := strings.TrimSpace(entry.Summary)
			if summary == "" {
				summary = "(无摘要)"
			}
			fmt.Fprintf(&b, "- [%s] %s\n", entry.Kind, summary)
		}
		if overflow := len(entries) - limit; overflow > 0 {
			fmt.Fprintf(&b, "- …(+%d 条)\n", overflow)
		}
	}

	b.WriteString("\n## 提案队列\n")
	if len(props) == 0 {
		b.WriteString("（无待决提案）\n")
	} else {
		for _, p := range props {
			fmt.Fprintf(&b, "- %s (urgency %d)\n", p.Title, p.Urgency)
			if body := trimBriefLine(p.Body, dailyBriefBodyLimit); body != "" {
				fmt.Fprintf(&b, "  %s\n", body)
			}
		}
	}

	b.WriteString("\n## 待审批\n")
	if len(holds) == 0 {
		b.WriteString("（无待审批项）\n")
	} else {
		for _, h := range holds {
			fmt.Fprintf(&b, "- %s — 执行于 %s\n", h.ToolName, h.ExecuteAt)
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

func trimBriefLine(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if s == "" || maxRunes <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

func (gw *Gateway) gatherDailyBrief(ctx context.Context, now time.Time) (string, error) {
	since := now.Add(-24 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	nowStr := now.UTC().Format("2006-01-02 15:04:05")

	var entries []world.JournalEntry
	if gw.toolSub == nil || gw.toolSub.WorldStore == nil {
		slog.Warn("brief: journal unavailable")
	} else if got, err := gw.toolSub.WorldStore.ListJournal(ctx, since, 50); err != nil {
		slog.Warn("brief: list journal failed", "err", err)
	} else {
		entries = got
	}

	var props []proposals.Proposal
	if gw.db == nil || gw.db.DB == nil {
		slog.Warn("brief: proposals unavailable")
	} else if got, err := proposals.NewStore(gw.db.DB).ListPending(ctx, now.Unix()); err != nil {
		slog.Warn("brief: list proposals failed", "err", err)
	} else {
		props = got
	}

	var holds []action.Hold
	if gw.toolSub == nil || gw.toolSub.ActionStore == nil {
		slog.Warn("brief: holds unavailable")
	} else if got, err := gw.toolSub.ActionStore.DueHolds(ctx, nowStr); err != nil {
		slog.Warn("brief: list holds failed", "err", err)
	} else {
		holds = got
	}

	return buildDailyBrief(now, entries, props, holds), nil
}

func (gw *Gateway) deliverDailyBrief(ctx context.Context) {
	notifier, target := gw.primaryNotifier()
	if notifier == nil {
		slog.Warn("brief: no notification channel")
		return
	}
	text, _ := gw.gatherDailyBrief(ctx, time.Now())
	if err := notifier.SendNotification(ctx, target, text); err != nil {
		slog.Warn("brief: send failed", "err", err)
	}
}

func (gw *Gateway) handleBrief(ctx context.Context, _ channel.Channel, _ channel.InboundMessage) (string, error) {
	return gw.gatherDailyBrief(ctx, time.Now())
}
