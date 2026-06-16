// Package proposals is the durable queue for the anticipation engine
// (DAIMON_BLUEPRINT.md §4.9): concrete next actions the agent expects the user
// will need but has not yet asked for. A sleep job writes proposals here by
// scanning upcoming commitments; delivery (Telegram inline accept/dismiss) and
// firing an episode on acceptance are later increments that read from and decide
// against this queue. The store is deliberately time-free — callers supply
// timestamps — so the job and tests stay deterministic.
package proposals

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// Proposal states. A proposal starts pending and reaches exactly one terminal
// state: accepted (user opted in — fires its action plan), dismissed (user said
// no), or expired (its window passed before any decision).
const (
	StatePending   = "pending"
	StateAccepted  = "accepted"
	StateDismissed = "dismissed"
	StateExpired   = "expired"
)

// Proposal action kinds. episode preserves the existing accept behavior: use
// ActionPlan as an episode goal. promote_skill accepts by deterministically
// promoting the staged skill draft named in ActionRef.
const (
	ActionKindEpisode      = "episode"
	ActionKindPromoteSkill = "promote_skill"
)

// Proposal is one anticipatory suggestion. ActionPlan is the episode goal fired
// if the user accepts an episode action; ActionKind/ActionRef type non-episode
// actions (for promote_skill, ActionRef is the staged draft slug); Urgency (0 low
// .. 3 urgent) orders the queue; CreatedAt, ExpiresAt, DecidedAt are epoch
// seconds (0 = unset/none).
type Proposal struct {
	ID               string
	Title            string
	Body             string
	ActionPlan       string
	Urgency          int
	SourceCommitment string
	State            string
	CreatedAt        int64
	ExpiresAt        int64
	DecidedAt        int64
	ActionKind       string
	ActionRef        string
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) ensure() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("proposals store unavailable")
	}
	return nil
}

// Create inserts a proposal. A blank ID is filled with "proposal_"+uuid and a
// blank State defaults to pending; CreatedAt is the caller's to set (the store
// never reads the clock, keeping it deterministic). The new id is written back
// into p only for the caller's convenience on the local copy.
func (s *Store) Create(ctx context.Context, p Proposal) error {
	if err := s.ensure(); err != nil {
		return err
	}
	if p.ID == "" {
		p.ID = "proposal_" + uuid.NewString()
	}
	if p.State == "" {
		p.State = StatePending
	}
	if p.ActionKind == "" {
		p.ActionKind = ActionKindEpisode
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO proposals
			(id, title, body, action_plan, urgency, source_commitment, state, created_at, expires_at, decided_at, action_kind, action_ref)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Title, p.Body, p.ActionPlan, p.Urgency, p.SourceCommitment, p.State, p.CreatedAt, p.ExpiresAt, p.DecidedAt, p.ActionKind, p.ActionRef)
	if err != nil {
		return fmt.Errorf("create proposal: %w", err)
	}
	return nil
}

// ListPending returns pending proposals still live at now (expires_at 0 meaning
// no expiry, or strictly after now), most urgent first then oldest first.
func (s *Store) ListPending(ctx context.Context, now int64) ([]Proposal, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, body, action_plan, urgency, source_commitment, state, created_at, expires_at, decided_at, action_kind, action_ref
		FROM proposals
		WHERE state = ? AND (expires_at = 0 OR expires_at > ?)
		ORDER BY urgency DESC, created_at ASC, id ASC`,
		StatePending, now)
	if err != nil {
		return nil, fmt.Errorf("list pending proposals: %w", err)
	}
	defer rows.Close()

	var out []Proposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, fmt.Errorf("scan proposal: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proposals: %w", err)
	}
	return out, nil
}

// ListUndelivered returns live pending proposals not yet pushed to the user
// (delivered_at = 0), in the same urgency-then-age order as ListPending. The
// delivery driver pushes these and marks each delivered, so a proposal is offered
// once rather than re-pushed every cycle.
func (s *Store) ListUndelivered(ctx context.Context, now int64) ([]Proposal, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, body, action_plan, urgency, source_commitment, state, created_at, expires_at, decided_at, action_kind, action_ref
		FROM proposals
		WHERE state = ? AND delivered_at = 0 AND (expires_at = 0 OR expires_at > ?)
		ORDER BY urgency DESC, created_at ASC, id ASC`,
		StatePending, now)
	if err != nil {
		return nil, fmt.Errorf("list undelivered proposals: %w", err)
	}
	defer rows.Close()

	var out []Proposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, fmt.Errorf("scan proposal: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proposals: %w", err)
	}
	return out, nil
}

// MarkDelivered stamps delivered_at on a proposal that has not been delivered yet.
// It is idempotent: a second mark (a re-pushed duplicate, a race) updates nothing
// and returns nil, so delivery accounting never double-counts. State is not
// constrained — a proposal decided between the push and the mark stays decided and
// is simply recorded as delivered.
func (s *Store) MarkDelivered(ctx context.Context, id string, at int64) error {
	if err := s.ensure(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE proposals SET delivered_at = ? WHERE id = ? AND delivered_at = 0`,
		at, id); err != nil {
		return fmt.Errorf("mark proposal delivered: %w", err)
	}
	return nil
}

// PendingTitles is the set of titles of proposals still LIVE at now (pending and
// not past their expiry), used by the sleep job to avoid queuing a duplicate of
// a proposal the user has not yet acted on. It applies the same live predicate as
// ListPending so an expired-but-undecided proposal (nothing transitions it to
// the expired state yet) does not block its title from being re-proposed once its
// window has passed; decided rows are likewise ignored, so a dismissed idea can
// resurface later if it becomes relevant again.
func (s *Store) PendingTitles(ctx context.Context, now int64) (map[string]bool, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT title FROM proposals WHERE state = ? AND (expires_at = 0 OR expires_at > ?)`,
		StatePending, now)
	if err != nil {
		return nil, fmt.Errorf("list pending titles: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			return nil, fmt.Errorf("scan pending title: %w", err)
		}
		out[title] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending titles: %w", err)
	}
	return out, nil
}

// PendingPromoteRefs is the set of live pending staged skill slugs already under
// a promote_skill proposal. The distill screen uses this to avoid re-proposing a
// draft while a human decision is still pending.
func (s *Store) PendingPromoteRefs(ctx context.Context, now int64) (map[string]bool, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT action_ref
		FROM proposals
		WHERE state = ? AND action_kind = ? AND (expires_at = 0 OR expires_at > ?) AND action_ref != ''`,
		StatePending, ActionKindPromoteSkill, now)
	if err != nil {
		return nil, fmt.Errorf("list pending promote refs: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, fmt.Errorf("scan pending promote ref: %w", err)
		}
		out[ref] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending promote refs: %w", err)
	}
	return out, nil
}

// RecentlyDismissedTitles is the set of titles the user dismissed at or after
// `since`. The anticipation job subtracts these from new proposals so a dismissed
// idea is not re-queued during its cooldown window — a dismissal lowers the
// proposal's recurrence rather than being a one-off no (§4.9 "被 dismiss 的同类
// 提案频次自动下降"). After the window the title may resurface if still relevant.
func (s *Store) RecentlyDismissedTitles(ctx context.Context, since int64) (map[string]bool, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT title FROM proposals WHERE state = ? AND decided_at >= ?`,
		StateDismissed, since)
	if err != nil {
		return nil, fmt.Errorf("list dismissed titles: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			return nil, fmt.Errorf("scan dismissed title: %w", err)
		}
		out[title] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dismissed titles: %w", err)
	}
	return out, nil
}

// RecentlyDismissedPromoteRefs is the set of promote_skill action refs the user
// dismissed at or after `since`. Distilled skill drafts are stably identified by
// slug/action_ref rather than by title, because the draft name in the title may
// change while the staged directory remains the same.
func (s *Store) RecentlyDismissedPromoteRefs(ctx context.Context, since int64) (map[string]bool, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT action_ref FROM proposals WHERE state = ? AND action_kind = ? AND decided_at >= ? AND action_ref != ''`,
		StateDismissed, ActionKindPromoteSkill, since)
	if err != nil {
		return nil, fmt.Errorf("list dismissed promote refs: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, fmt.Errorf("scan dismissed promote ref: %w", err)
		}
		out[ref] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dismissed promote refs: %w", err)
	}
	return out, nil
}

// Decide transitions a pending proposal to a terminal state (accepted, dismissed,
// or expired) and stamps decidedAt. It updates only a row still pending, so a
// second decision (a double-tap, a race) affects nothing and returns an error —
// the caller learns the proposal was already decided rather than silently
// re-deciding it.
func (s *Store) Decide(ctx context.Context, id, state string, decidedAt int64) error {
	if err := s.ensure(); err != nil {
		return err
	}
	switch state {
	case StateAccepted, StateDismissed, StateExpired:
	default:
		return fmt.Errorf("decide proposal: invalid terminal state %q", state)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE proposals SET state = ?, decided_at = ?
		WHERE id = ? AND state = ?`,
		state, decidedAt, id, StatePending)
	if err != nil {
		return fmt.Errorf("decide proposal: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("decide proposal: rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("proposal %q not found or already decided", id)
	}
	return nil
}

// Get loads a single proposal by id regardless of state. The decision UX carries
// only the proposal id in its callback (Telegram callback data is tightly
// bounded), so the coordinator reloads the row to recover the action plan it must
// fire on acceptance. Returns a wrapped sql.ErrNoRows when the id is unknown.
func (s *Store) Get(ctx context.Context, id string) (Proposal, error) {
	if err := s.ensure(); err != nil {
		return Proposal{}, err
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, title, body, action_plan, urgency, source_commitment, state, created_at, expires_at, decided_at, action_kind, action_ref
		FROM proposals
		WHERE id = ?`, id)
	p, err := scanProposal(row)
	if err != nil {
		return Proposal{}, fmt.Errorf("get proposal %q: %w", id, err)
	}
	return p, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProposal(row rowScanner) (Proposal, error) {
	var p Proposal
	err := row.Scan(
		&p.ID,
		&p.Title,
		&p.Body,
		&p.ActionPlan,
		&p.Urgency,
		&p.SourceCommitment,
		&p.State,
		&p.CreatedAt,
		&p.ExpiresAt,
		&p.DecidedAt,
		&p.ActionKind,
		&p.ActionRef,
	)
	return p, err
}
