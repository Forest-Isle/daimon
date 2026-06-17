package action

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
)

// Class is the reversibility category of an action, which determines how the
// action layer governs it.
type Class int

const (
	// Reversible actions (git-tracked file edits, world-model writes) execute
	// immediately and record an undo entry.
	Reversible Class = iota
	// Compensable actions (sending mail/messages, cancellable orders) go through
	// the hold queue so they can be recalled before they take effect.
	Compensable
	// Irreversible actions (payments, unrecoverable deletes, legal commitments)
	// require approval up to their trust ceiling, which never reaches full auto.
	Irreversible
)

func (c Class) String() string {
	switch c {
	case Reversible:
		return "reversible"
	case Compensable:
		return "compensable"
	case Irreversible:
		return "irreversible"
	default:
		return "unknown"
	}
}

// ParseClass converts a stored class string back into a Class.
func ParseClass(s string) (Class, error) {
	switch s {
	case "reversible":
		return Reversible, nil
	case "compensable":
		return Compensable, nil
	case "irreversible":
		return Irreversible, nil
	default:
		return 0, fmt.Errorf("unknown action class %q", s)
	}
}

// Level is the autonomy granted to a (class, context) pair. It rises with a
// verified track record and falls when the user corrects an action.
type Level int

const (
	// AskEvery requires approval for every action.
	AskEvery Level = iota
	// AskFirst requires approval only the first time, then trusts.
	AskFirst
	// HoldThenAuto executes automatically after the recall window.
	HoldThenAuto
	// FullAuto executes immediately with no hold.
	FullAuto
)

func (l Level) String() string {
	switch l {
	case AskEvery:
		return "ask_every"
	case AskFirst:
		return "ask_first"
	case HoldThenAuto:
		return "hold_then_auto"
	case FullAuto:
		return "full_auto"
	default:
		return "unknown"
	}
}

// UndoRecord describes how to reverse a completed reversible action.
type UndoRecord struct {
	ReceiptID string
	ToolName  string
	UndoSpec  string
	EpisodeID string
	ExpiresAt string
}

type UndoEntry struct {
	ReceiptID string
	ToolName  string
	UndoSpec  string
	EpisodeID string
	CreatedAt string
	UndoneAt  string
}

var (
	ErrUndoNotFound    = errors.New("undo entry not found")
	ErrUndoAlreadyDone = errors.New("undo entry already undone")
)

// Hold is a deferred compensable action awaiting its recall window.
type Hold struct {
	ID        string
	ReceiptID string
	ToolName  string
	Payload   string
	ExecuteAt string
	State     string
	CreatedAt string
}

// Store persists the trust ledger, undo journal, and hold queue.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// TrustLevel returns the autonomy level recorded for a (class, context) pair,
// defaulting to AskEvery when no history exists.
func (s *Store) TrustLevel(ctx context.Context, class Class, contextKey string) (Level, error) {
	if err := s.ensure(); err != nil {
		return AskEvery, err
	}
	var level int
	err := s.db.QueryRowContext(ctx,
		`SELECT level FROM trust_ledger WHERE action_class = ? AND context_key = ?`,
		class.String(), contextKey).Scan(&level)
	if errors.Is(err, sql.ErrNoRows) {
		return AskEvery, nil
	}
	if err != nil {
		return AskEvery, fmt.Errorf("read trust level: %w", err)
	}
	return Level(level), nil
}

// RecordAttempt logs one execution and, when the track record is clean,
// promotes the autonomy level by one step toward the class ceiling.
func (s *Store) RecordAttempt(ctx context.Context, class Class, contextKey string, verified bool) error {
	if err := s.ensure(); err != nil {
		return err
	}
	inc := 0
	if verified {
		inc = 1
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO trust_ledger (action_class, context_key, attempts, verified_ok, corrected, level, updated_at)
		VALUES (?, ?, 1, ?, 0, 0, CURRENT_TIMESTAMP)
		ON CONFLICT(action_class, context_key) DO UPDATE SET
			attempts = attempts + 1,
			verified_ok = verified_ok + ?,
			updated_at = CURRENT_TIMESTAMP`,
		class.String(), contextKey, inc, inc); err != nil {
		return fmt.Errorf("record attempt: %w", err)
	}

	var verifiedOK, corrected, level int
	err := s.db.QueryRowContext(ctx,
		`SELECT verified_ok, corrected, level FROM trust_ledger WHERE action_class = ? AND context_key = ?`,
		class.String(), contextKey).Scan(&verifiedOK, &corrected, &level)
	if err != nil {
		return fmt.Errorf("read trust row: %w", err)
	}

	// A single correction freezes promotion: autonomy is earned only by an
	// unbroken verified record.
	if corrected != 0 {
		return nil
	}
	ceiling := int(classCeiling(class))
	if level < ceiling && verifiedOK >= promotionThreshold(Level(level)) {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE trust_ledger SET level = ?, updated_at = CURRENT_TIMESTAMP WHERE action_class = ? AND context_key = ?`,
			level+1, class.String(), contextKey); err != nil {
			return fmt.Errorf("promote trust level: %w", err)
		}
	}
	return nil
}

// RecordCorrection logs a user correction and demotes the autonomy level by one
// step (floored at AskEvery).
func (s *Store) RecordCorrection(ctx context.Context, class Class, contextKey string) error {
	if err := s.ensure(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO trust_ledger (action_class, context_key, attempts, verified_ok, corrected, level, updated_at)
		VALUES (?, ?, 0, 0, 1, 0, CURRENT_TIMESTAMP)
		ON CONFLICT(action_class, context_key) DO UPDATE SET
			corrected = corrected + 1,
			level = max(level - 1, 0),
			updated_at = CURRENT_TIMESTAMP`,
		class.String(), contextKey); err != nil {
		return fmt.Errorf("record correction: %w", err)
	}
	return nil
}

// RecordUndo inserts an undo entry for a completed reversible action.
func (s *Store) RecordUndo(ctx context.Context, r UndoRecord) error {
	if err := s.ensure(); err != nil {
		return err
	}
	receiptID := r.ReceiptID
	if receiptID == "" {
		receiptID = "receipt_" + uuid.NewString()
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO undo_journal (receipt_id, tool_name, undo_spec, episode_id, expires_at) VALUES (?, ?, ?, ?, ?)`,
		receiptID, r.ToolName, r.UndoSpec, r.EpisodeID, nullable(r.ExpiresAt)); err != nil {
		return fmt.Errorf("record undo: %w", err)
	}
	return nil
}

func (s *Store) GetUndo(ctx context.Context, receiptID string) (UndoEntry, error) {
	if err := s.ensure(); err != nil {
		return UndoEntry{}, err
	}
	var entry UndoEntry
	var undoneAt sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT receipt_id, tool_name, undo_spec, episode_id, created_at, undone_at FROM undo_journal WHERE receipt_id = ?`,
		receiptID).Scan(&entry.ReceiptID, &entry.ToolName, &entry.UndoSpec, &entry.EpisodeID, &entry.CreatedAt, &undoneAt)
	if errors.Is(err, sql.ErrNoRows) {
		return UndoEntry{}, ErrUndoNotFound
	}
	if err != nil {
		return UndoEntry{}, fmt.Errorf("get undo: %w", err)
	}
	if undoneAt.Valid {
		entry.UndoneAt = undoneAt.String
	}
	return entry, nil
}

func (s *Store) ListUndoable(ctx context.Context, limit int) ([]UndoEntry, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT receipt_id, tool_name, undo_spec, episode_id, created_at, undone_at
		FROM undo_journal
		WHERE undone_at IS NULL
		ORDER BY created_at DESC, receipt_id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list undoable: %w", err)
	}
	defer rows.Close()

	var out []UndoEntry
	for rows.Next() {
		var entry UndoEntry
		var undoneAt sql.NullString
		if err := rows.Scan(&entry.ReceiptID, &entry.ToolName, &entry.UndoSpec, &entry.EpisodeID, &entry.CreatedAt, &undoneAt); err != nil {
			return nil, fmt.Errorf("scan undo: %w", err)
		}
		if undoneAt.Valid {
			entry.UndoneAt = undoneAt.String
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate undoable: %w", err)
	}
	return out, nil
}

func (s *Store) ListUndoableByEpisode(ctx context.Context, episodeID string) ([]UndoEntry, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	if episodeID == "" {
		return []UndoEntry{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT receipt_id, tool_name, undo_spec, episode_id, created_at, undone_at
		FROM undo_journal
		WHERE episode_id = ? AND undone_at IS NULL
		ORDER BY created_at DESC, receipt_id DESC`, episodeID)
	if err != nil {
		return nil, fmt.Errorf("list episode undoable: %w", err)
	}
	defer rows.Close()

	var out []UndoEntry
	for rows.Next() {
		var entry UndoEntry
		var undoneAt sql.NullString
		if err := rows.Scan(&entry.ReceiptID, &entry.ToolName, &entry.UndoSpec, &entry.EpisodeID, &entry.CreatedAt, &undoneAt); err != nil {
			return nil, fmt.Errorf("scan episode undo: %w", err)
		}
		if undoneAt.Valid {
			entry.UndoneAt = undoneAt.String
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate episode undoable: %w", err)
	}
	return out, nil
}

func (s *Store) Undo(ctx context.Context, root, receiptID string) error {
	entry, err := s.GetUndo(ctx, receiptID)
	if err != nil {
		return err
	}
	if entry.UndoneAt != "" {
		return ErrUndoAlreadyDone
	}
	if err := ExecuteUndo(ctx, root, entry); err != nil {
		return err
	}
	if err := s.MarkUndone(ctx, receiptID); err != nil {
		return err
	}
	return nil
}

func (s *Store) UndoEpisode(ctx context.Context, root, episodeID string) (reversed int, err error) {
	entries, err := s.ListUndoableByEpisode(ctx, episodeID)
	if err != nil {
		return 0, err
	}
	var errs []error
	for _, entry := range entries {
		if execErr := ExecuteUndo(ctx, root, entry); execErr != nil {
			errs = append(errs, fmt.Errorf("undo receipt %q: %w", entry.ReceiptID, execErr))
			continue
		}
		if markErr := s.MarkUndone(ctx, entry.ReceiptID); markErr != nil {
			errs = append(errs, fmt.Errorf("mark receipt %q undone: %w", entry.ReceiptID, markErr))
			continue
		}
		reversed++
	}
	return reversed, errors.Join(errs...)
}

// MarkUndone stamps an undo entry as reversed.
func (s *Store) MarkUndone(ctx context.Context, receiptID string) error {
	if err := s.ensure(); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE undo_journal SET undone_at = CURRENT_TIMESTAMP WHERE receipt_id = ?`, receiptID)
	if err != nil {
		return fmt.Errorf("mark undone: %w", err)
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return fmt.Errorf("undo receipt %q not found", receiptID)
	}
	return nil
}

// CreateHold enqueues a compensable action for deferred execution.
func (s *Store) CreateHold(ctx context.Context, h Hold) error {
	if err := s.ensure(); err != nil {
		return err
	}
	if h.ExecuteAt == "" {
		return errors.New("hold execute_at is required")
	}
	if h.ID == "" {
		h.ID = "hold_" + uuid.NewString()
	}
	if h.ReceiptID == "" {
		h.ReceiptID = "receipt_" + uuid.NewString()
	}
	if h.State == "" {
		h.State = "pending"
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO holds (id, receipt_id, tool_name, payload, execute_at, state) VALUES (?, ?, ?, ?, ?, ?)`,
		h.ID, h.ReceiptID, h.ToolName, h.Payload, h.ExecuteAt, h.State); err != nil {
		return fmt.Errorf("create hold: %w", err)
	}
	return nil
}

// DueHolds returns pending holds whose recall window has elapsed.
func (s *Store) DueHolds(ctx context.Context, now string) ([]Hold, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, receipt_id, tool_name, payload, execute_at, state, created_at
		FROM holds
		WHERE state = 'pending' AND execute_at <= ?
		ORDER BY execute_at ASC`, now)
	if err != nil {
		return nil, fmt.Errorf("list due holds: %w", err)
	}
	defer rows.Close()

	var out []Hold
	for rows.Next() {
		var h Hold
		if err := rows.Scan(&h.ID, &h.ReceiptID, &h.ToolName, &h.Payload, &h.ExecuteAt, &h.State, &h.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan hold: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate holds: %w", err)
	}
	return out, nil
}

// ListPendingHolds returns all holds still inside the recall queue.
func (s *Store) ListPendingHolds(ctx context.Context) ([]Hold, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, receipt_id, tool_name, payload, execute_at, state, created_at
		FROM holds
		WHERE state = 'pending'
		ORDER BY execute_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list pending holds: %w", err)
	}
	defer rows.Close()

	var out []Hold
	for rows.Next() {
		var h Hold
		if err := rows.Scan(&h.ID, &h.ReceiptID, &h.ToolName, &h.Payload, &h.ExecuteAt, &h.State, &h.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan pending hold: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending holds: %w", err)
	}
	return out, nil
}

// CountPendingHolds returns the number of holds still waiting in the recall queue.
func (s *Store) CountPendingHolds(ctx context.Context) (int, error) {
	if err := s.ensure(); err != nil {
		return 0, err
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM holds WHERE state = 'pending'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count pending holds: %w", err)
	}
	return n, nil
}

// ClaimHold atomically moves a pending hold into execution. It returns false
// when the hold was already recalled, claimed, executed, or never existed.
func (s *Store) ClaimHold(ctx context.Context, id string) (bool, error) {
	if err := s.ensure(); err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE holds SET state = 'executing' WHERE id = ? AND state = 'pending'`, id)
	if err != nil {
		return false, fmt.Errorf("claim hold: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim hold rows affected: %w", err)
	}
	return rows == 1, nil
}

// RecoverStaleHolds resets any hold left mid-claim ('executing') back to
// 'pending' so it is re-drained. It is called once at startup before the drain
// ticker starts: at that point nothing is in flight, so any 'executing' hold is
// necessarily orphaned by a crash between ClaimHold and MarkHoldState. Returns
// the number of holds recovered.
func (s *Store) RecoverStaleHolds(ctx context.Context) (int, error) {
	if err := s.ensure(); err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE holds SET state = 'pending' WHERE state = 'executing'`)
	if err != nil {
		return 0, fmt.Errorf("recover stale holds: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("recover stale holds rows affected: %w", err)
	}
	return int(n), nil
}

// MarkHoldState transitions a hold to a terminal state: executed (fired
// successfully), failed (fired but the tool errored), or recalled.
func (s *Store) MarkHoldState(ctx context.Context, id, state string) error {
	if err := s.ensure(); err != nil {
		return err
	}
	if state != "executed" && state != "recalled" && state != "failed" {
		return fmt.Errorf("invalid hold state %q", state)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE holds SET state = ? WHERE id = ?`, state, id)
	if err != nil {
		return fmt.Errorf("mark hold state: %w", err)
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return fmt.Errorf("hold %q not found", id)
	}
	return nil
}

// RecallHold cancels a still-pending hold. It errors if the hold already
// executed or does not exist.
func (s *Store) RecallHold(ctx context.Context, id string) error {
	if err := s.ensure(); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE holds SET state = 'recalled' WHERE id = ? AND state = 'pending'`, id)
	if err != nil {
		return fmt.Errorf("recall hold: %w", err)
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		var state string
		qerr := s.db.QueryRowContext(ctx, `SELECT state FROM holds WHERE id = ?`, id).Scan(&state)
		if errors.Is(qerr, sql.ErrNoRows) {
			return fmt.Errorf("hold %q not found", id)
		}
		if qerr != nil {
			return fmt.Errorf("recall hold: %w", qerr)
		}
		return fmt.Errorf("hold %q is %s, cannot recall", id, state)
	}
	return nil
}

func (s *Store) ensure() error {
	if s == nil || s.db == nil {
		return errors.New("action store unavailable")
	}
	return nil
}

// classCeiling caps autonomy by reversibility: irreversible actions never reach
// full auto, preserving a human gate for high-stakes actions.
func classCeiling(c Class) Level {
	if c == Irreversible {
		return HoldThenAuto
	}
	return FullAuto
}

// promotionThreshold is the verified count needed to leave a given level.
func promotionThreshold(level Level) int {
	switch level {
	case AskEvery:
		return 1
	case AskFirst:
		return 3
	case HoldThenAuto:
		return 10
	default:
		return math.MaxInt
	}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
