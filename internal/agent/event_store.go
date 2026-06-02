package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

// ExecutionEventStore persists and retrieves execution graph events.
type ExecutionEventStore interface {
	Append(ctx context.Context, event GraphEvent) error
	ListBySession(ctx context.Context, sessionID string) ([]GraphEvent, error)
	GetLatestState(ctx context.Context, sessionID string) (*GraphState, error)
	DeleteBySession(ctx context.Context, sessionID string) error
}

// SQLiteExecutionEventStore implements ExecutionEventStore backed by SQLite.
type SQLiteExecutionEventStore struct {
	db *store.DB
}

func NewSQLiteExecutionEventStore(db *store.DB) *SQLiteExecutionEventStore {
	return &SQLiteExecutionEventStore{db: db}
}

func (s *SQLiteExecutionEventStore) Append(ctx context.Context, event GraphEvent) error {
	id := event.ID
	if id == "" {
		id = fmt.Sprintf("%d", time.Now().UnixNano())
	}

	metaJSON, err := json.Marshal(event.Metadata)
	if err != nil {
		metaJSON = []byte("{}")
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO execution_events (
			id, session_id, node_type, transitioned_to, execution_path,
			input_snapshot, output_snapshot, metadata
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		event.SessionID,
		event.NodeType,
		event.TransitionedTo,
		event.ExecutionPath,
		event.InputSnapshot,
		event.OutputSnapshot,
		string(metaJSON),
	)
	return err
}

func (s *SQLiteExecutionEventStore) ListBySession(ctx context.Context, sessionID string) ([]GraphEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, node_type, transitioned_to, execution_path,
		        input_snapshot, output_snapshot, metadata, created_at
		 FROM execution_events
		 WHERE session_id = ?
		 ORDER BY created_at ASC`, sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var events []GraphEvent
	for rows.Next() {
		var event GraphEvent
		var metadataJSON string
		if err := rows.Scan(
			&event.ID,
			&event.SessionID,
			&event.NodeType,
			&event.TransitionedTo,
			&event.ExecutionPath,
			&event.InputSnapshot,
			&event.OutputSnapshot,
			&metadataJSON,
			&event.Timestamp,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(metadataJSON), &event.Metadata); err != nil {
			_ = json.Unmarshal([]byte("{}"), &event.Metadata)
		}
		events = append(events, event)
	}

	return events, rows.Err()
}

func (s *SQLiteExecutionEventStore) GetLatestState(ctx context.Context, sessionID string) (*GraphState, error) {
	events, err := s.ListBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, nil
	}

	first := events[0]
	last := events[len(events)-1]
	currentNode := last.TransitionedTo
	if currentNode == "" {
		currentNode = last.NodeType
	}
	return &GraphState{
		SessionID:     sessionID,
		CurrentNode:   currentNode,
		ExecutionPath: last.ExecutionPath,
		Iteration:     len(events),
		Events:        events,
		CreatedAt:     first.Timestamp,
		UpdatedAt:     last.Timestamp,
	}, nil
}

func (s *SQLiteExecutionEventStore) DeleteBySession(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM execution_events WHERE session_id = ?`, sessionID,
	)
	return err
}

var _ ExecutionEventStore = (*SQLiteExecutionEventStore)(nil)
var _ = sql.ErrNoRows
