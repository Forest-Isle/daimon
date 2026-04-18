package taskledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

type SQLiteTaskLedger struct {
	db *store.DB
}

func NewSQLiteTaskLedger(db *store.DB) *SQLiteTaskLedger {
	return &SQLiteTaskLedger{db: db}
}

func (s *SQLiteTaskLedger) Register(ctx context.Context, task Task) error {
	now := time.Now().UTC()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = now
	}
	if task.State == "" {
		task.State = TaskStatePending
	}
	if task.Kind == "" {
		task.Kind = TaskKindUserRequest
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO task_ledger (id, parent_id, kind, state, title, description, assignee, depends_on, created_at, updated_at, started_at, completed_at, heartbeat, result, metadata)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.ParentID, string(task.Kind), string(task.State),
		task.Title, task.Description, task.Assignee,
		encodeDependsOn(task.DependsOn), task.CreatedAt.UTC(), task.UpdatedAt.UTC(),
		timeToUTC(task.StartedAt), timeToUTC(task.CompletedAt), timeToUTC(task.Heartbeat),
		task.Result, encodeMetadata(task.Metadata),
	)
	return err
}

func (s *SQLiteTaskLedger) Get(ctx context.Context, id string) (*Task, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, parent_id, kind, state, title, description, assignee, depends_on, created_at, updated_at, started_at, completed_at, heartbeat, result, metadata
		 FROM task_ledger WHERE id = ?`, id)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("task %q not found", id)
	}
	return t, err
}

func (s *SQLiteTaskLedger) Update(ctx context.Context, task Task) error {
	task.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`UPDATE task_ledger SET parent_id=?, kind=?, state=?, title=?, description=?, assignee=?, depends_on=?, updated_at=?, started_at=?, completed_at=?, heartbeat=?, result=?, metadata=?
		 WHERE id=?`,
		task.ParentID, string(task.Kind), string(task.State),
		task.Title, task.Description, task.Assignee,
		encodeDependsOn(task.DependsOn), task.UpdatedAt.UTC(),
		timeToUTC(task.StartedAt), timeToUTC(task.CompletedAt), timeToUTC(task.Heartbeat),
		task.Result, encodeMetadata(task.Metadata),
		task.ID,
	)
	return err
}

func (s *SQLiteTaskLedger) List(ctx context.Context, filter TaskFilter) ([]Task, error) {
	query := `SELECT id, parent_id, kind, state, title, description, assignee, depends_on, created_at, updated_at, started_at, completed_at, heartbeat, result, metadata FROM task_ledger WHERE 1=1`
	var args []any

	if filter.State != nil {
		query += ` AND state = ?`
		args = append(args, string(*filter.State))
	}
	if filter.Kind != nil {
		query += ` AND kind = ?`
		args = append(args, string(*filter.Kind))
	}
	if filter.ParentID != nil {
		query += ` AND parent_id = ?`
		args = append(args, *filter.ParentID)
	}
	query += ` ORDER BY created_at ASC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanTasks(rows)
}

func (s *SQLiteTaskLedger) Cancel(ctx context.Context, id string, reason string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`WITH RECURSIVE descendants(id) AS (
			SELECT id FROM task_ledger WHERE id = ?
			UNION ALL
			SELECT tl.id FROM task_ledger tl JOIN descendants d ON tl.parent_id = d.id
		)
		UPDATE task_ledger SET state = ?, result = ?, updated_at = ?
		WHERE id IN (SELECT id FROM descendants) AND state NOT IN (?, ?)`,
		id, string(TaskStateCancelled), reason, now,
		string(TaskStateCompleted), string(TaskStateFailed),
	)
	return err
}

func (s *SQLiteTaskLedger) ClaimNext(ctx context.Context, kind TaskKind, assignee string) (*Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var taskID string
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM task_ledger WHERE state = ? AND kind = ? ORDER BY created_at ASC LIMIT 1`,
		string(TaskStatePending), string(kind),
	).Scan(&taskID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx,
		`UPDATE task_ledger SET state = ?, assignee = ?, started_at = ?, heartbeat = ?, updated_at = ? WHERE id = ?`,
		string(TaskStateRunning), assignee, now, now, now, taskID,
	)
	if err != nil {
		return nil, err
	}

	row := tx.QueryRowContext(ctx,
		`SELECT id, parent_id, kind, state, title, description, assignee, depends_on, created_at, updated_at, started_at, completed_at, heartbeat, result, metadata
		 FROM task_ledger WHERE id = ?`, taskID)
	task, err := scanTask(row)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *SQLiteTaskLedger) Heartbeat(ctx context.Context, id string) error {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`UPDATE task_ledger SET heartbeat = ?, updated_at = ? WHERE id = ? AND state = ?`,
		now, now, id, string(TaskStateRunning),
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("task %q not found or not running", id)
	}
	return nil
}

func (s *SQLiteTaskLedger) GetTree(ctx context.Context, rootID string) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx,
		`WITH RECURSIVE descendants(id) AS (
			SELECT id FROM task_ledger WHERE id = ?
			UNION ALL
			SELECT tl.id FROM task_ledger tl JOIN descendants d ON tl.parent_id = d.id
		)
		SELECT tl.id, tl.parent_id, tl.kind, tl.state, tl.title, tl.description, tl.assignee, tl.depends_on, tl.created_at, tl.updated_at, tl.started_at, tl.completed_at, tl.heartbeat, tl.result, tl.metadata
		FROM task_ledger tl WHERE tl.id IN (SELECT id FROM descendants)
		ORDER BY tl.created_at ASC`, rootID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanTasks(rows)
}

func (s *SQLiteTaskLedger) DetectStale(ctx context.Context, timeout time.Duration) ([]Task, error) {
	cutoff := time.Now().Add(-timeout).UTC()
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, parent_id, kind, state, title, description, assignee, depends_on, created_at, updated_at, started_at, completed_at, heartbeat, result, metadata
		 FROM task_ledger
		 WHERE state = ? AND COALESCE(heartbeat, started_at) < ?`,
		string(TaskStateRunning), cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanTasks(rows)
}

// --- helpers ---

func timeToUTC(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(row rowScanner) (*Task, error) {
	var t Task
	var kind, state, depsStr, metaStr string
	var startedAt, completedAt, heartbeat *time.Time

	if err := row.Scan(
		&t.ID, &t.ParentID, &kind, &state,
		&t.Title, &t.Description, &t.Assignee, &depsStr,
		&t.CreatedAt, &t.UpdatedAt, &startedAt, &completedAt, &heartbeat,
		&t.Result, &metaStr,
	); err != nil {
		return nil, err
	}

	t.Kind = TaskKind(kind)
	t.State = TaskState(state)
	t.StartedAt = startedAt
	t.CompletedAt = completedAt
	t.Heartbeat = heartbeat
	t.DependsOn = decodeDependsOn(depsStr)
	t.Metadata = decodeMetadata(metaStr)

	return &t, nil
}

func scanTasks(rows *sql.Rows) ([]Task, error) {
	var tasks []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *t)
	}
	return tasks, rows.Err()
}

func encodeDependsOn(deps []string) string {
	if len(deps) == 0 {
		return ""
	}
	return strings.Join(deps, ",")
}

func decodeDependsOn(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func encodeMetadata(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func decodeMetadata(s string) map[string]string {
	if s == "" {
		return nil
	}
	var m map[string]string
	if json.Unmarshal([]byte(s), &m) != nil {
		return nil
	}
	return m
}
