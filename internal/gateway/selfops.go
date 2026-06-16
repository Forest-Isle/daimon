package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"syscall"
	"time"

	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/attention"
	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/proposals"
	"github.com/Forest-Isle/daimon/internal/selfops"
	"github.com/Forest-Isle/daimon/internal/world"
)

const (
	selfopsProposalTitle           = "selfops health warnings"
	selfopsDefaultMaxSalvagedRate  = 0.10
	selfopsDefaultMaxRoutingMisses = 0
	selfopsDefaultMaxHoldsPending  = 20
	selfopsDefaultMinDiskFreePct   = 10.0
	selfopsDefaultMaxErrorCluster  = 5
)

var selfopsDefaultThresholds = selfops.Thresholds{
	MaxSalvagedRate: selfopsDefaultMaxSalvagedRate,
	// Routing-miss zero tolerance stays disabled until the signal is mature enough
	// to avoid false WakeUser alarms from sparse correction data.
	MaxRoutingMisses:    selfopsDefaultMaxRoutingMisses,
	MaxHoldsPending:     selfopsDefaultMaxHoldsPending,
	MinDiskFreePct:      selfopsDefaultMinDiskFreePct,
	MaxErrorClusterSize: selfopsDefaultMaxErrorCluster,
}

func (gw *Gateway) gatherHealthSignals(ctx context.Context, now time.Time) selfops.Signals {
	sig := selfops.Signals{DiskFreePct: 100}

	since := now.Add(-24 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	if gw.toolSub == nil || gw.toolSub.WorldStore == nil {
		slog.Warn("selfops: journal unavailable")
	} else if entries, err := gw.toolSub.WorldStore.ListJournal(ctx, since, 200); err != nil {
		slog.Warn("selfops: list journal failed", "err", err)
	} else {
		for _, entry := range entries {
			if entry.Kind != "outcome" {
				continue
			}
			sig.OutcomesTotal++
			if world.ClassifyOutcome(entry.Detail, entry.Summary) == world.OutcomeSalvaged {
				sig.Salvaged++
			}
		}
	}

	if gw.heart == nil || gw.heart.feedback == nil {
		slog.Warn("selfops: feedback unavailable")
	} else if recent, err := gw.heart.feedback.Recent(ctx, 200); err != nil {
		slog.Warn("selfops: list feedback failed", "err", err)
	} else {
		wakeUser := attention.WakeUser.String()
		for _, fb := range recent {
			if fb.ExpectedAction == wakeUser && fb.GivenAction != wakeUser {
				sig.RoutingMisses++
			}
		}
	}

	if gw.toolSub == nil || gw.toolSub.ActionStore == nil {
		slog.Warn("selfops: holds unavailable")
	} else if n, err := gw.toolSub.ActionStore.CountPendingHolds(ctx); err != nil {
		slog.Warn("selfops: count holds failed", "err", err)
	} else {
		sig.HoldsPending = n
	}

	if pct, err := diskFreePct(appdir.BaseDir()); err != nil {
		slog.Warn("selfops: disk stat failed", "err", err)
	} else {
		sig.DiskFreePct = pct
	}
	sig.ErrorClusters = selfops.ClusterErrors(selfops.Errors.Snapshot())

	return sig
}

func diskFreePct(path string) (float64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 100, fmt.Errorf("statfs %s: %w", path, err)
	}
	if st.Blocks == 0 {
		return 100, nil
	}
	available := float64(st.Bavail) * float64(st.Bsize)
	total := float64(st.Blocks) * float64(st.Bsize)
	return available / total * 100, nil
}

func (gw *Gateway) runHealthCheck(ctx context.Context) {
	now := time.Now()
	findings := selfops.Evaluate(gw.gatherHealthSignals(ctx, now), selfopsDefaultThresholds)
	var critical, warn []selfops.Finding
	for _, f := range findings {
		switch f.Severity {
		case selfops.SeverityCritical:
			critical = append(critical, f)
		case selfops.SeverityWarn:
			warn = append(warn, f)
		}
	}
	if len(critical) > 0 {
		gw.deliverHealthCritical(ctx, critical)
	}
	if len(warn) > 0 {
		gw.proposeHealthWarnings(ctx, now, warn)
	}
}

func (gw *Gateway) deliverHealthCritical(ctx context.Context, findings []selfops.Finding) {
	notifier, target := gw.primaryNotifier()
	if notifier == nil {
		slog.Warn("selfops: critical findings but no notification channel")
		return
	}
	text := "⚠️ selfops health\n" + renderHealthFindingLines(findings)
	if err := notifier.SendNotification(ctx, target, text); err != nil {
		slog.Warn("selfops: send critical health failed", "err", err)
	}
}

func (gw *Gateway) proposeHealthWarnings(ctx context.Context, now time.Time, findings []selfops.Finding) {
	if gw.db == nil || gw.db.DB == nil {
		slog.Warn("selfops: proposals unavailable")
		return
	}
	store := proposals.NewStore(gw.db.DB)
	pending, err := store.ListPending(ctx, now.Unix())
	if err != nil {
		slog.Warn("selfops: list proposals failed", "err", err)
		return
	}
	for _, p := range pending {
		if p.Title == selfopsProposalTitle {
			return
		}
	}
	if err := store.Create(ctx, proposals.Proposal{
		Title:     selfopsProposalTitle,
		Body:      renderHealthFindingLines(findings),
		Urgency:   1,
		CreatedAt: now.Unix(),
		ExpiresAt: now.Add(72 * time.Hour).Unix(),
	}); err != nil {
		slog.Warn("selfops: create warning proposal failed", "err", err)
	}
}

func (gw *Gateway) handleSelfops(ctx context.Context, _ channel.Channel, _ channel.InboundMessage) (string, error) {
	now := time.Now()
	sig := gw.gatherHealthSignals(ctx, now)
	findings := selfops.Evaluate(sig, selfopsDefaultThresholds)
	var b strings.Builder
	fmt.Fprintf(&b, "**Selfops Health** — %s\n\n", now.Format("2006-01-02 15:04"))
	b.WriteString("## Signals\n")
	fmt.Fprintf(&b, "- outcomes_total: %d\n", sig.OutcomesTotal)
	fmt.Fprintf(&b, "- salvaged: %d\n", sig.Salvaged)
	fmt.Fprintf(&b, "- salvaged_rate: %.2f\n", sig.SalvagedRate())
	fmt.Fprintf(&b, "- routing_misses: %d\n", sig.RoutingMisses)
	fmt.Fprintf(&b, "- holds_pending: %d\n", sig.HoldsPending)
	fmt.Fprintf(&b, "- disk_free_pct: %.1f\n", sig.DiskFreePct)
	if len(sig.ErrorClusters) > 0 {
		b.WriteString("- error_clusters:\n")
		for i, c := range sig.ErrorClusters {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "  - %q: %d\n", c.Key, c.Count)
		}
	}

	b.WriteString("\n## Findings\n")
	if len(findings) == 0 {
		b.WriteString("(none)\n")
	} else {
		b.WriteString(renderHealthFindingLines(findings))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func renderHealthFindingLines(findings []selfops.Finding) string {
	var b strings.Builder
	for _, f := range findings {
		fmt.Fprintf(&b, "- [%s] %s", f.Severity.String(), f.Title)
		if f.Detail != "" {
			fmt.Fprintf(&b, ": %s", f.Detail)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
