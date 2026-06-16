package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/proposals"
	"github.com/Forest-Isle/daimon/internal/skill"
	"github.com/Forest-Isle/daimon/internal/sleep"
	"github.com/Forest-Isle/daimon/internal/world"
)

// Promote proposals expire even though drafts do not: a long-unhandled or
// undelivered proposal should eventually stop blocking the same slug, allowing
// distill-screen to re-judge and re-propose the still-staged draft.
const promoteProposalTTLDays = 30

// worldCommitmentSource adapts the world store to sleep.commitmentLister. The due
// filter is applied in Go rather than via SQL: due_at is a free-form DATETIME the
// model writes in no enforced format, so a lexicographic SQL comparison is
// unsafe; parsing each value with a tolerant set of layouts is robust to that
// drift. Commitments with no parseable due date are not "due soon" and are
// dropped (overdue ones, due before now, are kept — they are exactly what to
// anticipate).
type worldCommitmentSource struct {
	world *world.Store
}

func (s worldCommitmentSource) DueCommitments(ctx context.Context, withinUnix int64) ([]sleep.CommitmentBrief, error) {
	commitments, err := s.world.ListCommitments(ctx, []string{"active"}, "")
	if err != nil {
		return nil, err
	}
	var out []sleep.CommitmentBrief
	for _, c := range commitments {
		due, ok := parseDueUnix(c.DueAt)
		if !ok || due > withinUnix {
			continue
		}
		out = append(out, sleep.CommitmentBrief{
			ID:    c.ID,
			Kind:  c.Kind,
			Title: c.Title,
			Body:  c.Body,
			DueAt: due,
		})
	}
	return out, nil
}

// zonedDueLayouts carry an explicit offset, so they parse unambiguously.
var zonedDueLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
}

// localDueLayouts carry no zone. The model is not constrained to one format and
// writes wall-clock dates the local user means in local time, so they are parsed
// in time.Local — not UTC (time.Parse's default), which would shift a zone-less
// date by the local offset and mis-bucket a commitment near the 72h horizon.
var localDueLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

func parseDueUnix(due string) (int64, bool) {
	due = strings.TrimSpace(due)
	if due == "" {
		return 0, false
	}
	for _, layout := range zonedDueLayouts {
		if t, err := time.Parse(layout, due); err == nil {
			return t.Unix(), true
		}
	}
	for _, layout := range localDueLayouts {
		if t, err := time.ParseInLocation(layout, due, time.Local); err == nil {
			return t.Unix(), true
		}
	}
	return 0, false
}

// proposalsStoreSink adapts the proposals store to sleep.proposalWriter. The job
// is clock-free; this boundary stamps created_at from the gateway's clock.
type proposalsStoreSink struct {
	store *proposals.Store
	now   func() int64
}

func (s proposalsStoreSink) PendingTitles(ctx context.Context, now int64) (map[string]bool, error) {
	return s.store.PendingTitles(ctx, now)
}

func (s proposalsStoreSink) RecentlyDismissedTitles(ctx context.Context, since int64) (map[string]bool, error) {
	return s.store.RecentlyDismissedTitles(ctx, since)
}

func (s proposalsStoreSink) Add(ctx context.Context, items []sleep.ProposedItem) error {
	createdAt := s.now()
	for _, it := range items {
		if err := s.store.Create(ctx, proposals.Proposal{
			Title:            it.Title,
			Body:             it.Body,
			ActionPlan:       it.ActionPlan,
			Urgency:          it.Urgency,
			SourceCommitment: it.SourceCommitment,
			CreatedAt:        createdAt,
			ExpiresAt:        it.ExpiresAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s proposalsStoreSink) PendingPromoteRefs(ctx context.Context, now int64) (map[string]bool, error) {
	return s.store.PendingPromoteRefs(ctx, now)
}

func (s proposalsStoreSink) RecentlyDismissedPromoteRefs(ctx context.Context, since int64) (map[string]bool, error) {
	return s.store.RecentlyDismissedPromoteRefs(ctx, since)
}

func (s proposalsStoreSink) AddPromote(ctx context.Context, items []sleep.PromoteProposal) error {
	createdAt := s.now()
	expiresAt := createdAt + int64(promoteProposalTTLDays)*86400
	for _, it := range items {
		if err := s.store.Create(ctx, proposals.Proposal{
			Title:      it.Title,
			Body:       it.Body,
			ActionKind: proposals.ActionKindPromoteSkill,
			ActionRef:  it.Slug,
			CreatedAt:  createdAt,
			ExpiresAt:  expiresAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

type stagedDraftSource struct {
	dir string
}

func defaultStagedDraftSource() stagedDraftSource {
	return stagedDraftSource{dir: appdir.SkillsStagingDir()}
}

func (s stagedDraftSource) StagedDrafts(context.Context) ([]sleep.DraftCandidate, error) {
	infos, err := skill.ListDrafts(s.dir)
	if err != nil {
		return nil, err
	}
	out := make([]sleep.DraftCandidate, 0, len(infos))
	for _, info := range infos {
		if info.Status != "valid" {
			continue
		}
		draft, err := skill.ValidateDraft(filepath.Join(s.dir, info.Slug, "SKILL.md"))
		if err != nil {
			continue
		}
		body, err := draft.Content()
		if err != nil {
			continue
		}
		out = append(out, sleep.DraftCandidate{
			Slug:        info.Slug,
			Name:        info.Name,
			Description: info.Description,
			Episodes:    info.Episodes,
			Body:        body,
		})
	}
	return out, nil
}
