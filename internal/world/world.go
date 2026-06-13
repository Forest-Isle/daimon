package world

import (
	"context"
	"database/sql"
	"encoding/json"
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

// ApplyOutcome applies world writes from an episode outcome, stamps episodeID,
// and appends one idempotent journal entry for the outcome summary. salvaged
// records whether the Outcome was framework-recovered (the model never called
// episode_close), which the journal detail captures for the salvaged-rate metric.
func (s *Store) ApplyOutcome(ctx context.Context, episodeID string, muts []Mutation, summary string, salvaged bool) error {
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
	if err := appendOutcomeJournal(ctx, tx, episodeID, summary, salvaged); err != nil {
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

func appendOutcomeJournal(ctx context.Context, exec sqlExecer, episodeID string, summary string, salvaged bool) error {
	detail := ""
	if salvaged {
		detail = "salvaged=true"
	}
	_, err := exec.ExecContext(ctx, `
		INSERT OR IGNORE INTO journal
			(id, episode_id, kind, summary, detail)
		VALUES (?, ?, ?, ?, ?)`,
		"journal_outcome_"+episodeID, episodeID, "outcome", summary, detail)
	if err != nil {
		return fmt.Errorf("append outcome journal: %w", err)
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
