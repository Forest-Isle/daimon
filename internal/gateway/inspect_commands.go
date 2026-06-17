package gateway

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Forest-Isle/daimon/internal/action"
	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/proposals"
	"github.com/Forest-Isle/daimon/internal/replay"
)

func (gw *Gateway) handleEpisodes(ctx context.Context, _ channel.Channel, _ channel.InboundMessage) (string, error) {
	if gw == nil || gw.toolSub == nil || gw.toolSub.WorldStore == nil {
		return "Episodes unavailable.", nil
	}
	entries, err := gw.toolSub.WorldStore.ListOutcomes(ctx, 15)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "No episodes recorded.", nil
	}

	var b strings.Builder
	b.WriteString("Episodes\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "OCCURRED_AT\tEPISODE\tSUMMARY")
	for _, entry := range entries {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", entry.OccurredAt, entry.EpisodeID, entry.Summary)
	}
	_ = tw.Flush()
	return strings.TrimRight(b.String(), "\n"), nil
}

func (gw *Gateway) handleTrust(ctx context.Context, _ channel.Channel, _ channel.InboundMessage) (string, error) {
	if gw == nil || gw.toolSub == nil || gw.toolSub.ActionStore == nil {
		return "Trust ledger unavailable.", nil
	}
	entries, err := gw.toolSub.ActionStore.ListTrust(ctx)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "No trust ledger entries.", nil
	}

	var b strings.Builder
	b.WriteString("Trust Ledger\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CLASS\tCONTEXT\tLEVEL\tVERIFIED\tCORRECTED")
	for _, entry := range entries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d/%d\t%d\n",
			entry.ActionClass,
			entry.ContextKey,
			action.Level(entry.Level).String(),
			entry.VerifiedOK,
			entry.Attempts,
			entry.Corrected)
	}
	_ = tw.Flush()
	return strings.TrimRight(b.String(), "\n"), nil
}

func (gw *Gateway) handleHolds(ctx context.Context, _ channel.Channel, _ channel.InboundMessage) (string, error) {
	if gw == nil || gw.toolSub == nil || gw.toolSub.ActionStore == nil {
		return "Holds unavailable.", nil
	}
	holds, err := gw.toolSub.ActionStore.ListPendingHolds(ctx)
	if err != nil {
		return "", err
	}
	if len(holds) == 0 {
		return "No pending holds.", nil
	}

	var b strings.Builder
	b.WriteString("Pending Holds\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTOOL\tEXECUTE_AT")
	for _, hold := range holds {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", hold.ID, hold.ToolName, hold.ExecuteAt)
	}
	_ = tw.Flush()
	return strings.TrimRight(b.String(), "\n"), nil
}

func (gw *Gateway) handleProposals(ctx context.Context, _ channel.Channel, _ channel.InboundMessage) (string, error) {
	if gw == nil || gw.db == nil || gw.db.DB == nil {
		return "Proposals unavailable.", nil
	}
	pending, err := proposals.NewStore(gw.db.DB).ListPending(ctx, time.Now().Unix())
	if err != nil {
		return "", err
	}
	if len(pending) == 0 {
		return "No pending proposals.", nil
	}

	var b strings.Builder
	b.WriteString("Pending Proposals\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TITLE\tURGENCY\tEXPIRES")
	for _, proposal := range pending {
		fmt.Fprintf(tw, "%s\t%d\t%s\n", proposal.Title, proposal.Urgency, formatInspectUnix(proposal.ExpiresAt))
	}
	_ = tw.Flush()
	return strings.TrimRight(b.String(), "\n"), nil
}

func (gw *Gateway) handleReplay(ctx context.Context, _ channel.Channel, _ channel.InboundMessage) (string, error) {
	_ = ctx
	sessions, skipped, err := replay.LoadDir(filepath.Join(appdir.BaseDir(), "replays"))
	if err != nil {
		return "Replay unavailable.", nil
	}
	report := replay.Analyze(sessions, skipped)

	var b strings.Builder
	b.WriteString("Replay Summary\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "sessions\t%d\n", report.Sessions)
	fmt.Fprintf(tw, "exchanges\t%d\n", report.Exchanges)
	fmt.Fprintf(tw, "tool_calls\t%d\n", report.ToolCalls)
	fmt.Fprintf(tw, "tool_failures\t%d\n", report.ToolFailures)
	fmt.Fprintf(tw, "abnormal_stops\t%d\n", report.AbnormalStops)
	fmt.Fprintf(tw, "max_token_stops\t%d\n", report.MaxTokenStops)
	fmt.Fprintf(tw, "salvaged\t%d\n", report.Salvaged)
	fmt.Fprintf(tw, "skipped_lines\t%d\n", report.SkippedLines)
	_ = tw.Flush()
	return strings.TrimRight(b.String(), "\n"), nil
}

func formatInspectUnix(ts int64) string {
	if ts == 0 {
		return "none"
	}
	return time.Unix(ts, 0).Local().Format("2006-01-02 15:04")
}
