package agent

import (
	"context"
	"database/sql"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

// TaskCheckpoint captures the state of a cognitive loop at a given point,
// allowing interrupted tasks to be resumed.
type TaskCheckpoint struct {
	ID               string
	SessionID        string
	SubTaskIndex     int
	ObservationsJSON string
	PlanJSON         string
	CreatedAt        string
}

// CheckpointStore persists and retrieves task checkpoints.
type CheckpointStore interface {
	Save(ctx context.Context, cp *TaskCheckpoint) error
	Load(ctx context.Context, sessionID string) (*TaskCheckpoint, error)
	Delete(ctx context.Context, sessionID string) error
}

// SQLiteCheckpointStore implements CheckpointStore backed by SQLite.
type SQLiteCheckpointStore struct {
	db *store.DB
}

func NewSQLiteCheckpointStore(db *store.DB) *SQLiteCheckpointStore {
	return &SQLiteCheckpointStore{db: db}
}

func (s *SQLiteCheckpointStore) Save(ctx context.Context, cp *TaskCheckpoint) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO task_checkpoints (id, session_id, subtask_index, observations_json, plan_json)
		 VALUES (?, ?, ?, ?, ?)`,
		cp.ID, cp.SessionID, cp.SubTaskIndex, cp.ObservationsJSON, cp.PlanJSON,
	)
	return err
}

func (s *SQLiteCheckpointStore) Load(ctx context.Context, sessionID string) (*TaskCheckpoint, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, session_id, subtask_index, observations_json, plan_json, created_at
		 FROM task_checkpoints WHERE session_id = ?`, sessionID,
	)
	cp := &TaskCheckpoint{}
	if err := row.Scan(&cp.ID, &cp.SessionID, &cp.SubTaskIndex, &cp.ObservationsJSON, &cp.PlanJSON, &cp.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return cp, nil
}

func (s *SQLiteCheckpointStore) Delete(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM task_checkpoints WHERE session_id = ?`, sessionID,
	)
	return err
}
