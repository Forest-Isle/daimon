package world

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

type Commitment struct {
	ID            string `json:"id,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Title         string `json:"title,omitempty"`
	Body          string `json:"body,omitempty"`
	State         string `json:"state,omitempty"`
	Horizon       string `json:"horizon,omitempty"`
	SourceEpisode string `json:"source_episode,omitempty"`
	DueAt         string `json:"due_at,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

type JournalEntry struct {
	ID         string `json:"id,omitempty"`
	EpisodeID  string `json:"episode_id,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Detail     string `json:"detail,omitempty"`
	OccurredAt string `json:"occurred_at,omitempty"`
	RollupID   string `json:"rollup_id,omitempty"`
}

// Mutation is one element of an episode Outcome's WorldWrites.
type Mutation struct {
	Op     string          `json:"op,omitempty"`
	Target string          `json:"target,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Apply(ctx context.Context, episodeID string, muts []Mutation) error {
	if err := s.ensure(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin world transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := applyMutations(ctx, tx, episodeID, muts); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit world transaction: %w", err)
	}
	committed = true
	return nil
}

// OutcomeMeta is the per-episode metadata stamped onto the outcome journal
// marker's detail field, for downstream consumers (distill candidacy, ROI).
// Salvaged marks a framework-recovered episode (the model never called
// episode_close). AutoClosed marks a framework-closed no-tool conversational turn
// (clean delivered value, but not a distill candidate). ToolFailures is how many of the episode's tool calls returned
// an error — a clean-execution signal: 0 means every tool call the episode made
// succeeded. UnverifiedActions is how many of the episode's governed action calls
// were not verified this run (§4.8): 0 means every governed action earned
// objective trust (or it took none). All are observational, never affecting
// control flow.
type OutcomeMeta struct {
	Salvaged          bool
	AutoClosed        bool
	ToolFailures      int
	UnverifiedActions int
	// ParentEpisodeID is the parent episode id for a subagent episode; empty for top-level episodes.
	ParentEpisodeID string
	// ValueCreatedUSD is the episode's self-reported USD value; 0 means unreported.
	ValueCreatedUSD float64
}

// ApplyOutcome applies world writes from an episode outcome, stamps episodeID,
// and appends one idempotent journal entry for the outcome summary. meta records
// per-episode signals (salvaged, tool-failure count) which the journal detail
// captures for the salvaged-rate metric and distill candidacy.
func (s *Store) ApplyOutcome(ctx context.Context, episodeID string, muts []Mutation, summary string, meta OutcomeMeta) error {
	if err := s.ensure(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin world transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Claim the outcome marker first: its row id is deterministic in episodeID, so
	// a second ApplyOutcome for the same episode (a re-delivery the kernel's
	// OutcomeExists skip did not catch — e.g. a concurrent or direct double
	// dispatch) inserts nothing and is treated as already-applied. This keeps the
	// world write idempotent at the truth layer, not just at the kernel: mutations
	// (some non-idempotent, e.g. commitment.create) run only on the first claim.
	claimed, err := claimOutcomeJournal(ctx, tx, episodeID, summary, meta)
	if err != nil {
		return err
	}
	if !claimed {
		return nil // outcome already applied by the original run; do not re-apply mutations
	}
	if err := applyMutations(ctx, tx, episodeID, muts); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit world transaction: %w", err)
	}
	committed = true
	return nil
}

func (s *Store) CreateCommitment(ctx context.Context, c Commitment) error {
	if err := s.ensure(); err != nil {
		return err
	}
	return createCommitment(ctx, s.db, c)
}

func (s *Store) UpdateCommitment(ctx context.Context, id string, set map[string]any) error {
	if err := s.ensure(); err != nil {
		return err
	}
	return updateCommitment(ctx, s.db, id, set, "", false)
}

func (s *Store) ListCommitments(ctx context.Context, states []string, dueBefore string) ([]Commitment, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	return s.listCommitments(ctx, states, dueBefore, 0)
}

// OutcomeExists reports whether the episode with the given id has already
// committed its outcome. The outcome journal row (id "journal_outcome_<id>") is
// written atomically with the episode's world writes in ApplyOutcome, so its
// presence means the whole episode completed. A re-delivered trigger (heart's
// at-least-once replay after a crash before the event was marked routed) uses
// this to skip an already-finished episode instead of re-running it.
func (s *Store) OutcomeExists(ctx context.Context, episodeID string) (bool, error) {
	if err := s.ensure(); err != nil {
		return false, err
	}
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM journal WHERE id = ?`, "journal_outcome_"+episodeID).Scan(&n); err != nil {
		return false, fmt.Errorf("check outcome exists: %w", err)
	}
	return n > 0, nil
}

// OutcomeParentEpisodeID returns the parent episode id recorded on an episode's
// outcome journal row (§4.3 parent linkage), or "" when the episode has no parent
// (a top-level episode) or has not yet committed an outcome. Lets a consumer
// resolve the parent→child episode tree by child episode id.
func (s *Store) OutcomeParentEpisodeID(ctx context.Context, episodeID string) (string, error) {
	if err := s.ensure(); err != nil {
		return "", err
	}
	var parentEpisodeID string
	if err := s.db.QueryRowContext(ctx,
		`SELECT parent_episode_id FROM journal WHERE id = ?`, "journal_outcome_"+episodeID).Scan(&parentEpisodeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("get outcome parent episode id: %w", err)
	}
	return parentEpisodeID, nil
}

// JournalExists reports whether a journal row with the given id exists. It lets
// callers de-duplicate by a deterministic id independently of any recency window
// (e.g. the distill job, which must not re-record a candidate whose prior entry
// has scrolled out of a bounded ListJournal scan).
func (s *Store) JournalExists(ctx context.Context, id string) (bool, error) {
	if err := s.ensure(); err != nil {
		return false, err
	}
	if id == "" {
		return false, nil
	}
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM journal WHERE id = ?`, id).Scan(&n); err != nil {
		return false, fmt.Errorf("check journal exists: %w", err)
	}
	return n > 0, nil
}

func (s *Store) AppendJournal(ctx context.Context, entry JournalEntry) error {
	if err := s.ensure(); err != nil {
		return err
	}
	return appendJournal(ctx, s.db, entry)
}

func (s *Store) ListJournal(ctx context.Context, sinceOccurredAt string, limit int) ([]JournalEntry, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	query := `
		SELECT id, episode_id, kind, summary, detail, occurred_at, rollup_id
		FROM journal`
	var args []any
	if sinceOccurredAt != "" {
		query += ` WHERE occurred_at >= ?`
		args = append(args, sinceOccurredAt)
	}
	query += ` ORDER BY occurred_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list journal: %w", err)
	}
	defer rows.Close()

	var out []JournalEntry
	for rows.Next() {
		entry, err := scanJournalEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("scan journal entry: %w", err)
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate journal: %w", err)
	}
	return out, nil
}

// ListOutcomes returns up to limit episode-outcome journal entries (kind="outcome"),
// newest first. Unlike ListJournal it is window-independent over outcomes: the
// distill detection job needs to see recurring patterns across many episodes, and a
// fixed all-kinds slice lets accumulating decision/fact/correction rows crowd
// outcomes out so a pattern that recurs across time is never co-visible in one scan.
// Filtering to kind="outcome" in SQL keeps the last N outcomes regardless of other
// journal growth.
func (s *Store) ListOutcomes(ctx context.Context, limit int) ([]JournalEntry, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, episode_id, kind, summary, detail, occurred_at, rollup_id
		FROM journal
		WHERE kind = 'outcome'
		ORDER BY occurred_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list outcomes: %w", err)
	}
	defer rows.Close()

	var out []JournalEntry
	for rows.Next() {
		entry, err := scanJournalEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("scan outcome entry: %w", err)
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outcomes: %w", err)
	}
	return out, nil
}

// ListDistillCandidatesWithoutDraft returns up to limit distill-candidate journal
// entries that do NOT yet have a draft marker, oldest first so the longest-waiting
// candidate is promoted before newer ones. A candidate is a kind="decision" entry
// whose summary begins "distill candidate: " (written by the distill sleep job); its
// draft marker is the entry with id "distill_draft_<candidate id>" (written by the
// distill-promote job after it stages or skips the candidate). This is
// window-independent: it filters in SQL over the whole journal, so a candidate is
// never starved by journal growth pushing it out of a recent-N slice. The
// "distill candidate: " / "distill_draft_" prefixes are the on-disk contract shared
// with internal/sleep; a regression there is caught by this method's test.
func (s *Store) ListDistillCandidatesWithoutDraft(ctx context.Context, limit int) ([]JournalEntry, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.episode_id, c.kind, c.summary, c.detail, c.occurred_at, c.rollup_id
		FROM journal c
		WHERE c.kind = 'decision'
		  AND c.summary LIKE 'distill candidate: %'
		  AND NOT EXISTS (SELECT 1 FROM journal m WHERE m.id = 'distill_draft_' || c.id)
		ORDER BY c.occurred_at ASC, c.id ASC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list distill candidates: %w", err)
	}
	defer rows.Close()

	var out []JournalEntry
	for rows.Next() {
		entry, err := scanJournalEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("scan distill candidate: %w", err)
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate distill candidates: %w", err)
	}
	return out, nil
}

// UnrolledBeyond returns journal entries eligible to be folded into a rollup:
// regular (non-fact, non-rollup) entries not yet rolled up, EXCEPT the most
// recent keepRecent of them, which stay raw so the recent window keeps full
// detail. Facts (upserted singletons like the self-digest) and prior rollups are
// never folded. Results are oldest-first (a natural reading order for a summary);
// limit caps how many are folded in one pass.
func (s *Store) UnrolledBeyond(ctx context.Context, keepRecent, limit int) ([]JournalEntry, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	if keepRecent < 0 {
		keepRecent = 0
	}
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, episode_id, kind, summary, detail, occurred_at, rollup_id
		FROM journal
		WHERE rollup_id = '' AND kind NOT IN ('fact', 'rollup')
		ORDER BY occurred_at DESC, id DESC
		LIMIT ? OFFSET ?`, limit, keepRecent)
	if err != nil {
		return nil, fmt.Errorf("list unrolled journal: %w", err)
	}
	defer rows.Close()

	var out []JournalEntry
	for rows.Next() {
		entry, err := scanJournalEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("scan journal entry: %w", err)
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate journal: %w", err)
	}
	// Reverse to oldest-first so a summary reads chronologically.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// ListFacts returns durable facts (journal entries of kind=fact), most recent
// first, capped at limit (default 200). Reconcile uses these to detect
// contradictions and near-duplicates among the agent's accumulated facts.
func (s *Store) ListFacts(ctx context.Context, limit int) ([]JournalEntry, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, episode_id, kind, summary, detail, occurred_at, rollup_id
		FROM journal
		WHERE kind = 'fact'
		ORDER BY occurred_at DESC, id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list facts: %w", err)
	}
	defer rows.Close()

	var out []JournalEntry
	for rows.Next() {
		entry, err := scanJournalEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("scan fact: %w", err)
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate facts: %w", err)
	}
	return out, nil
}

// Rollup folds the given journal entries into one summary: it appends a single
// kind="rollup" entry and stamps each folded entry's rollup_id so they are not
// folded again. It is atomic (one transaction) and non-destructive — folded
// entries are tagged, never deleted, so their detail stays recoverable. Returns
// the new rollup id.
func (s *Store) Rollup(ctx context.Context, summary string, foldedIDs []string) (string, error) {
	if err := s.ensure(); err != nil {
		return "", err
	}
	if strings.TrimSpace(summary) == "" {
		return "", fmt.Errorf("rollup requires a summary")
	}
	if len(foldedIDs) == 0 {
		return "", fmt.Errorf("rollup requires at least one entry to fold")
	}
	rollupID := "rollup_" + uuid.NewString()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin rollup transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := appendJournal(ctx, tx, JournalEntry{ID: rollupID, Kind: "rollup", Summary: "Journal rollup", Detail: summary}); err != nil {
		return "", err
	}
	args := make([]any, 0, len(foldedIDs)+1)
	args = append(args, rollupID)
	for _, id := range foldedIDs {
		args = append(args, id)
	}
	// Re-assert eligibility in the UPDATE itself: only fold rows still untagged and
	// still regular (not a fact/rollup), so a stale id can never overwrite an
	// existing tag or fold an upserted singleton. The whole rollup is rolled back
	// if the eligible set changed since selection, so the summary never claims to
	// cover entries it did not tag.
	res, err := tx.ExecContext(ctx,
		`UPDATE journal SET rollup_id = ? WHERE rollup_id = '' AND kind NOT IN ('fact', 'rollup') AND id IN (`+placeholders(len(foldedIDs))+`)`, args...)
	if err != nil {
		return "", fmt.Errorf("tag folded entries: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("rollup rows affected: %w", err)
	}
	if int(affected) != len(foldedIDs) {
		return "", fmt.Errorf("rollup eligibility changed: tagged %d of %d entries", affected, len(foldedIDs))
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit rollup: %w", err)
	}
	committed = true
	return rollupID, nil
}

func (s *Store) CommitmentsDigest(ctx context.Context, dueWithin string) (string, error) {
	if err := s.ensure(); err != nil {
		return "", err
	}
	commitments, err := s.listCommitments(ctx, []string{"active"}, dueWithin, 20)
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(commitments))
	for _, c := range commitments {
		due := c.DueAt
		if due == "" {
			due = "no due"
		}
		lines = append(lines, fmt.Sprintf("%s/%s/%s/%s", compactLine(c.Kind), compactLine(c.Title), compactLine(c.State), compactLine(due)))
	}
	return strings.Join(lines, "\n"), nil
}

type Identity struct {
	Dir string
}

func (i Identity) Digest() string {
	data, err := os.ReadFile(filepath.Join(i.Dir, "digest.md"))
	if err != nil {
		return ""
	}
	return string(data)
}

func (i Identity) EnsureDir() error {
	if err := os.MkdirAll(i.Dir, 0o755); err != nil {
		return fmt.Errorf("ensure identity dir: %w", err)
	}
	return nil
}

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (s *Store) ensure() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("world store unavailable")
	}
	return nil
}

func applyMutations(ctx context.Context, exec sqlExecer, episodeID string, muts []Mutation) error {
	for _, mut := range muts {
		switch mut.Op {
		case "commitment.create":
			var c Commitment
			if err := json.Unmarshal(mut.Body, &c); err != nil {
				return fmt.Errorf("decode commitment.create: %w", err)
			}
			c.SourceEpisode = episodeID
			if err := createCommitment(ctx, exec, c); err != nil {
				return err
			}
		case "commitment.update":
			set, err := decodeUpdate(mut.Body)
			if err != nil {
				return fmt.Errorf("decode commitment.update: %w", err)
			}
			id := strings.TrimSpace(mut.Target)
			if id == "" {
				id = stringFromUpdate(set, "id", "ID")
			}
			delete(set, "id")
			delete(set, "ID")
			if err := updateCommitment(ctx, exec, id, set, episodeID, true); err != nil {
				return err
			}
		case "journal.append":
			var entry JournalEntry
			if err := json.Unmarshal(mut.Body, &entry); err != nil {
				return fmt.Errorf("decode journal.append: %w", err)
			}
			entry.EpisodeID = episodeID
			if err := appendJournal(ctx, exec, entry); err != nil {
				return err
			}
		case "fact.upsert":
			var entry JournalEntry
			if err := json.Unmarshal(mut.Body, &entry); err != nil {
				return fmt.Errorf("decode fact.upsert: %w", err)
			}
			entry.EpisodeID = episodeID
			if err := upsertFact(ctx, exec, entry); err != nil {
				return err
			}
		case "fact.delete":
			if err := deleteFact(ctx, exec, strings.TrimSpace(mut.Target)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown world mutation op %q", mut.Op)
		}
	}
	return nil
}

func createCommitment(ctx context.Context, exec sqlExecer, c Commitment) error {
	if c.ID == "" {
		c.ID = "commitment_" + uuid.NewString()
	}
	if c.State == "" {
		c.State = "active"
	}
	_, err := exec.ExecContext(ctx, `
		INSERT INTO commitments
			(id, kind, title, body, state, due_at, horizon, source_episode)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Kind, c.Title, c.Body, c.State, nullableDue(c.DueAt), c.Horizon, c.SourceEpisode)
	if err != nil {
		return fmt.Errorf("create commitment: %w", err)
	}
	return nil
}

func updateCommitment(ctx context.Context, exec sqlExecer, id string, set map[string]any, sourceEpisode string, stampSource bool) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("commitment id is required")
	}
	normalized := make(map[string]any, len(set)+1)
	for key, value := range set {
		column, ok := commitmentUpdateColumn(key)
		if !ok {
			return fmt.Errorf("commitment field %q is not updatable", key)
		}
		if column == "due_at" {
			value = nullableDueValue(value)
		}
		normalized[column] = value
	}
	if stampSource {
		normalized["source_episode"] = sourceEpisode
	}

	order := []string{"title", "body", "state", "due_at", "horizon", "source_episode"}
	sets := make([]string, 0, len(normalized)+1)
	args := make([]any, 0, len(normalized)+1)
	for _, column := range order {
		value, ok := normalized[column]
		if !ok {
			continue
		}
		sets = append(sets, column+" = ?")
		args = append(args, value)
	}
	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id)

	query := `UPDATE commitments SET ` + strings.Join(sets, ", ") + ` WHERE id = ?`
	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update commitment: %w", err)
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return fmt.Errorf("commitment %q not found", id)
	}
	return nil
}

// claimOutcomeJournal inserts the deterministic outcome marker for an episode and
// reports whether this call inserted it (true) or it already existed (false). The
// id is "journal_outcome_<episodeID>" with INSERT OR IGNORE, so it doubles as an
// idempotency claim: a false return means the episode's outcome was already
// recorded and the caller must not re-apply its world writes.
func claimOutcomeJournal(ctx context.Context, exec sqlExecer, episodeID string, summary string, meta OutcomeMeta) (bool, error) {
	// Detail encodes one per-episode signal, by precedence (salvaged > auto_closed >
	// tool_failures > unverified_actions). Each is independently an exclusion signal
	// downstream (distill), so which one wins the single detail slot does not change
	// any decision — it only picks what is displayed (auto_closed never co-occurs
	// with the others: a no-tool turn has no tool failures or actions). Salvaged
	// is preserved byte-exact for backward compatibility; a fully clean, fully
	// verified episode records "" exactly as before.
	detail := ""
	switch {
	case meta.Salvaged:
		detail = "salvaged=true"
	case meta.AutoClosed:
		detail = "auto_closed=true"
	case meta.ToolFailures > 0:
		detail = fmt.Sprintf("tool_failures=%d", meta.ToolFailures)
	case meta.UnverifiedActions > 0:
		detail = fmt.Sprintf("unverified_actions=%d", meta.UnverifiedActions)
	}
	res, err := exec.ExecContext(ctx, `
		INSERT OR IGNORE INTO journal
			(id, episode_id, kind, summary, detail, parent_episode_id, value_created_usd)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"journal_outcome_"+episodeID, episodeID, "outcome", summary, detail, meta.ParentEpisodeID, meta.ValueCreatedUSD)
	if err != nil {
		return false, fmt.Errorf("append outcome journal: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("append outcome journal: rows affected: %w", err)
	}
	return affected > 0, nil
}

// upsertFact records a durable fact as a journal entry of kind=fact, indexed for
// retrieval by the journal_fts trigger. When the caller supplies an id, an
// existing fact with that id is replaced (delete-then-insert, so the FTS delete
// trigger fires — INSERT OR REPLACE would not with recursive_triggers off,
// leaving a stale index row). Without an id it is appended.
func upsertFact(ctx context.Context, exec sqlExecer, entry JournalEntry) error {
	entry.Kind = "fact"
	if strings.TrimSpace(entry.Summary) == "" {
		return fmt.Errorf("fact.upsert requires a summary")
	}
	if entry.ID != "" {
		// Guard the replacement delete with kind='fact', symmetric with deleteFact:
		// fact.upsert must only ever replace a fact, never an append-only audit row
		// (outcome/decision/correction). A caller-supplied id is untrusted — a model
		// can put any id in an episode_close fact.upsert WorldWrite. If that id points
		// at a non-fact row, this delete matches nothing and the insert below collides
		// on the primary key, failing the whole transaction. Fail-closed: the audit
		// row is never destroyed (invariants #1/#4).
		if _, err := exec.ExecContext(ctx, `DELETE FROM journal WHERE id = ? AND kind = 'fact'`, entry.ID); err != nil {
			return fmt.Errorf("upsert fact (delete prior): %w", err)
		}
	}
	return appendJournal(ctx, exec, entry)
}

// deleteFact removes a fact (kind=fact) by id, firing the journal_fts delete
// trigger so the retrieval index drops it too. The kind='fact' guard is a
// safety floor: outcomes, decisions, and corrections are append-only audit and
// must never be removed even if a stray target id points at one. A missing id is
// a no-op (no error): reconcile may supersede the same stale fact across
// overlapping passes, and a re-delivered sleep cycle must not fail because the
// fact is already gone. id must be non-empty so a blank target cannot match.
func deleteFact(ctx context.Context, exec sqlExecer, id string) error {
	if id == "" {
		return fmt.Errorf("fact.delete requires a non-empty target id")
	}
	if _, err := exec.ExecContext(ctx, `DELETE FROM journal WHERE id = ? AND kind = 'fact'`, id); err != nil {
		return fmt.Errorf("delete fact %q: %w", id, err)
	}
	return nil
}

func appendJournal(ctx context.Context, exec sqlExecer, entry JournalEntry) error {
	if entry.ID == "" {
		entry.ID = "journal_" + uuid.NewString()
	}
	if entry.OccurredAt == "" {
		_, err := exec.ExecContext(ctx, `
			INSERT INTO journal
				(id, episode_id, kind, summary, detail, rollup_id)
			VALUES (?, ?, ?, ?, ?, ?)`,
			entry.ID, entry.EpisodeID, entry.Kind, entry.Summary, entry.Detail, entry.RollupID)
		if err != nil {
			return fmt.Errorf("append journal: %w", err)
		}
		return nil
	}
	_, err := exec.ExecContext(ctx, `
		INSERT INTO journal
			(id, episode_id, kind, summary, detail, occurred_at, rollup_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.EpisodeID, entry.Kind, entry.Summary, entry.Detail, entry.OccurredAt, entry.RollupID)
	if err != nil {
		return fmt.Errorf("append journal: %w", err)
	}
	return nil
}

func (s *Store) listCommitments(ctx context.Context, states []string, dueBefore string, limit int) ([]Commitment, error) {
	query := `
		SELECT id, kind, title, body, state, horizon, source_episode,
		       COALESCE(due_at, ''), created_at, updated_at
		FROM commitments`
	var where []string
	var args []any
	if len(states) > 0 {
		where = append(where, "state IN ("+placeholders(len(states))+")")
		for _, state := range states {
			args = append(args, state)
		}
	}
	if dueBefore != "" {
		where = append(where, "due_at IS NOT NULL AND due_at <= ?")
		args = append(args, dueBefore)
	}
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	query += ` ORDER BY due_at IS NULL, due_at ASC, updated_at DESC, title ASC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list commitments: %w", err)
	}
	defer rows.Close()

	var out []Commitment
	for rows.Next() {
		c, err := scanCommitment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan commitment: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate commitments: %w", err)
	}
	return out, nil
}

func scanCommitment(row rowScanner) (Commitment, error) {
	var c Commitment
	err := row.Scan(
		&c.ID,
		&c.Kind,
		&c.Title,
		&c.Body,
		&c.State,
		&c.Horizon,
		&c.SourceEpisode,
		&c.DueAt,
		&c.CreatedAt,
		&c.UpdatedAt,
	)
	return c, err
}

func scanJournalEntry(row rowScanner) (JournalEntry, error) {
	var entry JournalEntry
	err := row.Scan(
		&entry.ID,
		&entry.EpisodeID,
		&entry.Kind,
		&entry.Summary,
		&entry.Detail,
		&entry.OccurredAt,
		&entry.RollupID,
	)
	return entry, err
}

func decodeUpdate(body json.RawMessage) (map[string]any, error) {
	if len(body) == 0 {
		return map[string]any{}, nil
	}
	var set map[string]any
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, err
	}
	if set == nil {
		set = map[string]any{}
	}
	return set, nil
}

func stringFromUpdate(set map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := set[key]
		if !ok {
			continue
		}
		if s, ok := value.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func commitmentUpdateColumn(key string) (string, bool) {
	switch key {
	case "title", "Title":
		return "title", true
	case "body", "Body":
		return "body", true
	case "state", "State":
		return "state", true
	case "due_at", "dueAt", "DueAt":
		return "due_at", true
	case "horizon", "Horizon":
		return "horizon", true
	default:
		return "", false
	}
}

func nullableDue(due string) any {
	if due == "" {
		return nil
	}
	return due
}

func nullableDueValue(value any) any {
	if value == nil {
		return nil
	}
	if s, ok := value.(string); ok && s == "" {
		return nil
	}
	return value
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

func compactLine(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}
